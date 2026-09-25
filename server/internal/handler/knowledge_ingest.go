package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/knowledge/chunk"
	"github.com/jamshidtulaganov/agora/server/internal/knowledge/extract"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Reading a knowledge document into sections, in the background. A document
// is claimed by one worker at a time (ClaimKnowledgeDocForProcessing); a
// server that dies mid-read leaves it claimable again after 10 minutes, and
// ResumeKnowledgeIngest picks those up on boot and every few minutes.

// knowledgeIngestSlots caps concurrent reads: PDF and spreadsheet parsing is
// CPU- and memory-heavy, and the database plan is small.
var knowledgeIngestSlots = make(chan struct{}, 2)

const knowledgeIngestTimeout = 5 * time.Minute

// enqueueKnowledgeIngest reads a document in the background.
func (h *Handler) enqueueKnowledgeIngest(docID pgtype.UUID) {
	go func() {
		knowledgeIngestSlots <- struct{}{}
		defer func() { <-knowledgeIngestSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), knowledgeIngestTimeout)
		defer cancel()
		h.ingestKnowledgeDoc(ctx, docID)
	}()
}

// ResumeKnowledgeIngest re-queues documents a previous process left
// half-read, now and every five minutes until ctx ends.
func (h *Handler) ResumeKnowledgeIngest(ctx context.Context) {
	sweep := func() {
		ids, err := h.Queries.ListStaleProcessingKnowledgeDocs(ctx)
		if err != nil {
			slog.Warn("knowledge: list stale documents", "error", err)
			return
		}
		for _, id := range ids {
			h.enqueueKnowledgeIngest(id)
		}
	}
	go func() {
		sweep()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
}

// ingestKnowledgeDoc reads one claimed document into sections. Every failure
// ends in a terminal status with a message a person can act on.
func (h *Handler) ingestKnowledgeDoc(ctx context.Context, docID pgtype.UUID) {
	doc, err := h.Queries.ClaimKnowledgeDocForProcessing(ctx, docID)
	if err != nil {
		return // not processing, or another worker holds it
	}
	finish := func(status, message string, parsed extract.Document, chunks []chunk.Chunk) {
		chars := 0
		for _, c := range chunks {
			chars += len([]rune(c.Body))
		}
		if _, err := h.Queries.FinishKnowledgeDoc(ctx, db.FinishKnowledgeDocParams{
			ID: doc.ID, Status: status, Error: message,
			PageCount: int32(parsed.PageCount), CharCount: int32(chars), ChunkCount: int32(len(chunks)),
		}); err != nil {
			slog.Warn("knowledge: finish document", "doc_id", uuidToString(doc.ID), "error", err)
		}
		h.publish(protocol.EventKnowledgeUpdated, uuidToString(doc.WorkspaceID), "system", "", map[string]any{
			"doc_id": uuidToString(doc.ID), "status": status,
		})
	}

	data, filename, contentType, err := h.knowledgeDocBytes(ctx, doc)
	if err != nil {
		slog.Warn("knowledge: load document", "doc_id", uuidToString(doc.ID), "error", err)
		finish("failed", "The file couldn't be opened. Upload it again.", extract.Document{}, nil)
		return
	}
	parsed, err := extract.Extract(ctx, filename, contentType, data)
	if err != nil {
		finish("failed", knowledgeExtractMessage(err), extract.Document{}, nil)
		return
	}
	if parsed.Scanned {
		finish("needs_ocr", "This PDF is a scan with no text in it. Upload a version with selectable text.", parsed, nil)
		return
	}
	chunks := chunk.Split(parsed, chunk.Options{})
	if len(chunks) == 0 {
		finish("failed", "No text was found in this file.", parsed, nil)
		return
	}
	if err := h.replaceKnowledgeChunks(ctx, doc, chunks); err != nil {
		slog.Warn("knowledge: store sections", "doc_id", uuidToString(doc.ID), "error", err)
		finish("failed", "Something went wrong while saving this document. Try Reprocess.", parsed, nil)
		return
	}
	finish("ready", "", parsed, chunks)
}

// knowledgeDocBytes loads what the document is made of: the note's markdown,
// or the uploaded file from storage.
func (h *Handler) knowledgeDocBytes(ctx context.Context, doc db.KnowledgeDoc) ([]byte, string, string, error) {
	if doc.Source == "note" {
		return []byte(doc.NoteBody), doc.Title + ".md", "text/markdown", nil
	}
	if !doc.AttachmentID.Valid || h.Storage == nil {
		return nil, "", "", errors.New("no stored file")
	}
	att, err := h.Queries.GetAttachment(ctx, db.GetAttachmentParams{ID: doc.AttachmentID, WorkspaceID: doc.WorkspaceID})
	if err != nil {
		return nil, "", "", fmt.Errorf("attachment: %w", err)
	}
	rc, err := h.Storage.GetReader(ctx, h.Storage.KeyFromURL(att.Url))
	if err != nil {
		return nil, "", "", fmt.Errorf("storage: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, knowledgeMaxFileBytes+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read: %w", err)
	}
	return data, att.Filename, att.ContentType, nil
}

func knowledgeExtractMessage(err error) string {
	switch {
	case errors.Is(err, extract.ErrUnsupported):
		return "This file type isn't supported. Use PDF, Word (.docx), Excel (.xlsx), CSV, Markdown or text."
	case errors.Is(err, extract.ErrTooLarge):
		return "This file has more text than one document can hold. Split it into smaller files."
	case errors.Is(err, context.DeadlineExceeded):
		return "Reading this file took too long. Try a smaller file."
	default:
		return "This file couldn't be read: " + clipRunes(err.Error(), 200)
	}
}

// replaceKnowledgeChunks swaps a document's sections in one transaction, so
// a reprocess never leaves it half-searchable.
func (h *Handler) replaceKnowledgeChunks(ctx context.Context, doc db.KnowledgeDoc, chunks []chunk.Chunk) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	if err := qtx.DeleteKnowledgeChunksForDoc(ctx, doc.ID); err != nil {
		return err
	}
	rows := make([]db.InsertKnowledgeChunksParams, 0, len(chunks))
	for _, c := range chunks {
		rows = append(rows, db.InsertKnowledgeChunksParams{
			DocID: doc.ID, WorkspaceID: doc.WorkspaceID, Ord: int32(c.Ord),
			HeadingPath: c.HeadingPath, Location: c.Location, Body: c.Body,
		})
	}
	if _, err := qtx.InsertKnowledgeChunks(ctx, rows); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
