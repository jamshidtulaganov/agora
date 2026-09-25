package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The retrieval eval (docs/workspace-knowledge-plan.md §11): realistic
// department documents, questions phrased the way people ask them, and the
// section that answers each. The gate is "the right section is in the top 5"
// for at least 80% of questions. Misses are logged so a ranking change shows
// exactly what it broke or fixed. Cross-language questions (asked in one
// language about a document in another) are reported but not gated — they
// are what Phase 3's embeddings are for.

var knowledgeEvalDocs = map[string]string{
	"Collections SOP": `# Collections SOP

## Overview
This procedure covers how the Collections team recovers overdue balances on fuel card accounts.
Every case is worked in the order: contact, payment plan, write-off review, legal escalation.

## Contacting the customer
Call the account owner within 2 business days after the balance becomes 15 days past due.
Leave a voicemail and send the reminder email template "Past due — first notice".
If there is no answer after three attempts, send the certified letter.

## Payment plans
Agents may offer a payment plan of up to 6 months without approval.
Plans longer than 6 months or balances above $10,000 need approval from the Collections Manager.
Record every plan in Zoho CRM on the Collection Case with the schedule and the first due date.

## Write-offs
A balance can be written off only after 120 days past due and a failed payment plan.
Write-offs above $5,000 require written approval from the Collections Manager and the Finance Director.
Attach the call log, the certified letter receipt and the last statement to the case before requesting approval.

## Legal escalation
Accounts over 90 days past due with a balance above $2,500 go to small claims court.
Prepare the demand letter, the signed credit application and the statement history for the attorney.

## Credit bureau disputes
When a customer disputes a Trans Union report, pause collection activity on the account.
Respond to the dispute within 30 days with the account history, then resume.`,

	"Refund policy": `# Refund policy

## Card fee refunds
Monthly card fees are refunded only if the card was never activated during the month.
Refunds are credited to the next statement, never paid by check.

## Transaction disputes and chargebacks
A customer has 60 days from the transaction date to dispute a fuel transaction.
Collect the receipt and the station name, open a chargeback in the processor portal, and keep the account active while it is reviewed.

## Late fees
A late fee of $50 is charged when a payment arrives more than 5 days after the due date.
One late fee per year may be waived by a team lead if the customer asks.`,

	"Fee schedule": `# Fee schedule

## Standard fees
| Service | Fee | Notes |
|---|---|---|
| Money code issued | $3.50 | per code |
| Card replacement | $5.00 | lost or stolen |
| Monthly card fee | $7.95 | per active card |
| Account setup | $75.00 | one time |
| Returned payment | $35.00 | NSF or closed account |`,

	"Регламент работы с должниками": `# Регламент работы с должниками

## Первый контакт
Позвоните клиенту в течение двух рабочих дней после того, как задолженность просрочена на 15 дней.

## Реструктуризация долга
Менеджер по взысканию может предложить рассрочку до шести месяцев без согласования с руководителем.

## Списание задолженности
Списание возможно только после 120 дней просрочки и письменного согласования финансового директора.`,
}

type knowledgeEvalCase struct {
	question string
	doc      string // expected document title
	heading  string // expected heading (substring of the heading path)
	crossLang bool  // asked in a different language than the document
}

var knowledgeEvalCases = []knowledgeEvalCase{
	{"when should we call a customer who is past due?", "Collections SOP", "Contacting the customer", false},
	{"what do I do if the customer doesn't answer after three calls", "Collections SOP", "Contacting the customer", false},
	{"how long can a payment plan be without approval", "Collections SOP", "Payment plans", false},
	{"who approves a payment plan over $10,000", "Collections SOP", "Payment plans", false},
	{"can I write off a $7,000 balance? who needs to approve", "Collections SOP", "Write-offs", false},
	{"what documents do I attach before a write-off request", "Collections SOP", "Write-offs", false},
	{"when does an account go to small claims court", "Collections SOP", "Legal escalation", false},
	{"what does the attorney need for a legal case", "Collections SOP", "Legal escalation", false},
	{"customer disputed their Trans Union report, what now", "Collections SOP", "Credit bureau disputes", false},
	{"how fast must we respond to a credit bureau dispute", "Collections SOP", "Credit bureau disputes", false},
	{"is the monthly card fee refundable", "Refund policy", "Card fee refunds", false},
	{"how many days does a customer have to dispute a fuel transaction", "Refund policy", "Transaction disputes", false},
	{"how do I open a chargeback", "Refund policy", "Transaction disputes", false},
	{"how much is the late fee and can it be waived", "Refund policy", "Late fees", false},
	{"how much does a money code cost", "Fee schedule", "Standard fees", false},
	{"what is the fee for a returned payment", "Fee schedule", "Standard fees", false},
	{"account setup fee", "Fee schedule", "Standard fees", false},
	{"когда звонить клиенту с просрочкой", "Регламент работы с должниками", "Первый контакт", false},
	{"рассрочка без согласования руководителя", "Регламент работы с должниками", "Реструктуризация", false},
	{"кто согласует списание задолженности", "Регламент работы с должниками", "Списание", false},
	// Cross-language: reported, not gated (Phase 3).
	{"кто утверждает списание больше 5000 долларов", "Collections SOP", "Write-offs", true},
	{"who approves writing off a debt (russian doc)", "Регламент работы с должниками", "Списание", true},
}

func TestKnowledgeRetrievalEval(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-knowledge-eval", "owner")
	wsUUID := parseUUID(wsID)

	for title, body := range knowledgeEvalDocs {
		var docID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO knowledge_doc (workspace_id, title, source, note_body)
			VALUES ($1, $2, 'note', $3) RETURNING id
		`, wsID, title, body).Scan(&docID); err != nil {
			t.Fatalf("seed %q: %v", title, err)
		}
		testHandler.ingestKnowledgeDoc(ctx, parseUUID(docID))
		var status, errText string
		var chunks int
		if err := testPool.QueryRow(ctx, `SELECT status, error, chunk_count FROM knowledge_doc WHERE id = $1`, docID).Scan(&status, &errText, &chunks); err != nil || status != "ready" || chunks == 0 {
			t.Fatalf("%q not ready: status=%s chunks=%d err=%q %v", title, status, chunks, errText, err)
		}
	}

	var gated, gatedHits, cross, crossHits int
	var misses []string
	start := time.Now()
	for _, c := range knowledgeEvalCases {
		hits, err := testHandler.searchKnowledge(ctx, wsUUID, c.question, 5)
		if err != nil {
			t.Fatalf("search %q: %v", c.question, err)
		}
		found := false
		for _, h := range hits {
			if h.DocTitle == c.doc && strings.Contains(h.HeadingPath, c.heading) {
				found = true
				break
			}
		}
		if c.crossLang {
			cross++
			if found {
				crossHits++
			}
			continue
		}
		gated++
		if found {
			gatedHits++
			continue
		}
		var got []string
		for _, h := range hits {
			got = append(got, h.DocTitle+" › "+h.HeadingPath)
		}
		misses = append(misses, fmt.Sprintf("%q → want %s › %s, got %v", c.question, c.doc, c.heading, got))
	}
	for _, m := range misses {
		t.Logf("miss: %s", m)
	}
	recall := float64(gatedHits) / float64(gated)
	t.Logf("knowledge eval: top-5 recall %.0f%% (%d/%d), cross-language %d/%d (not gated), %d searches in %s",
		recall*100, gatedHits, gated, crossHits, cross, len(knowledgeEvalCases), time.Since(start).Round(time.Millisecond))
	if recall < 0.8 {
		t.Fatalf("top-5 recall %.0f%% is below the 80%% gate", recall*100)
	}
}

func TestKnowledgeSearchIsWorkspaceScoped(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	mine := createMcpTestWorkspace(t, ctx, "handler-tests-knowledge-mine", "owner")
	other := createMcpTestWorkspace(t, ctx, "handler-tests-knowledge-other", "owner")
	var docID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO knowledge_doc (workspace_id, title, source, note_body)
		VALUES ($1, 'Secret pricing', 'note', $2) RETURNING id
	`, other, "# Secret pricing\n\n## Discounts\nThe partner discount is 14 cents per gallon.").Scan(&docID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	testHandler.ingestKnowledgeDoc(ctx, parseUUID(docID))

	if hits, _ := testHandler.searchKnowledge(ctx, parseUUID(mine), "partner discount per gallon", 5); len(hits) != 0 {
		t.Fatalf("search leaked another workspace's document: %+v", hits)
	}
	if hits, _ := testHandler.searchKnowledge(ctx, parseUUID(other), "partner discount per gallon", 5); len(hits) == 0 {
		t.Fatal("search found nothing in the document's own workspace")
	}
	// Removed documents stop being searchable.
	if _, err := testPool.Exec(ctx, `UPDATE knowledge_doc SET archived_at = now() WHERE id = $1`, docID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if hits, _ := testHandler.searchKnowledge(ctx, parseUUID(other), "partner discount per gallon", 5); len(hits) != 0 {
		t.Fatalf("an archived document is still searchable: %+v", hits)
	}
}
