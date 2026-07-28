package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

// `agora telegram` — how a running agent speaks in its own Telegram groups.
//
// Delivered as a CLI subcommand rather than an MCP tool on purpose. The daemon
// already puts this binary on every agent's PATH with its task token, so this
// is the same surface the agent uses for `agora issue` and `agora comment`;
// the one MCP server Agora ships (agora-qa) was reached by under 1% of tasks,
// which is a poor bet for a capability meant to be used mid-run.
//
// The bot token is never handed over. The agent names a chat, the server holds
// the credential and sends. And it can only name a chat already bound to it —
// the same list that decides who may instruct the agent decides where it may
// speak, so granting a room and letting the agent talk there stay one decision.
//
// There is no edit and no delete. A message an agent has posted is part of the
// room's record, and letting it rewrite or erase that would make the transcript
// unreliable in exactly the situation where someone is reconstructing what an
// agent did.

var telegramCmd = &cobra.Command{
	Use:   "telegram",
	Short: "Speak in the Telegram groups this agent is bound to",
	Long: "Post to a Telegram group as this agent.\n\n" +
		"Only chats already bound to the agent can be reached. Run\n" +
		"`agora telegram chats` to see them.",
}

var telegramChatsCmd = &cobra.Command{
	Use:   "chats",
	Short: "List the chats this agent may post to",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		var result struct {
			BotUsername string `json:"bot_username"`
			Chats       []struct {
				ChatID  string `json:"chat_id"`
				Default bool   `json:"default"`
			} `json:"chats"`
		}
		if err := client.GetJSON(cmd.Context(), "/api/agents/me/telegram/chats", &result); err != nil {
			return err
		}
		if len(result.Chats) == 0 {
			fmt.Fprintln(os.Stdout, "No chats bound to this agent.")
			return nil
		}
		fmt.Fprintf(os.Stdout, "@%s\n", result.BotUsername)
		for _, c := range result.Chats {
			suffix := ""
			if c.Default {
				suffix = "  (default)"
			}
			fmt.Fprintf(os.Stdout, "  %s%s\n", c.ChatID, suffix)
		}
		return nil
	},
}

var (
	telegramSendChat  string
	telegramSendText  string
	telegramSendStdin bool
)

var telegramSendCmd = &cobra.Command{
	Use:   "send [text]",
	Short: "Post a message to a bound Telegram chat",
	Long: "Post a message as this agent.\n\n" +
		"With no --chat the agent's default chat is used. A long message should\n" +
		"come in on stdin with --stdin: a shell argument mangles newlines and\n" +
		"quoting.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		text := strings.TrimSpace(strings.Join(args, " "))
		if telegramSendText != "" {
			text = strings.TrimSpace(telegramSendText)
		}
		if text == "" && telegramSendStdin {
			data, readErr := io.ReadAll(os.Stdin)
			if readErr != nil {
				return fmt.Errorf("read stdin: %w", readErr)
			}
			text = strings.TrimSpace(string(data))
		}
		if text == "" {
			return fmt.Errorf("nothing to send: pass text as an argument, --text, or --stdin")
		}

		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		body := map[string]any{"text": text}
		if strings.TrimSpace(telegramSendChat) != "" {
			body["chat_id"] = strings.TrimSpace(telegramSendChat)
		}
		var result struct {
			ChatID string `json:"chat_id"`
		}
		if err := client.PostJSON(cmd.Context(), "/api/agents/me/telegram/send", body, &result); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Sent to %s\n", result.ChatID)
		return nil
	},
}

var (
	telegramAskChat    string
	telegramAskOptions []string
	telegramAskTimeout int
)

// telegramAskPollInterval is how often the CLI checks for an answer. Slow on
// purpose: a human decision takes minutes, and a tight loop would spend the
// whole wait hammering the API for a value that changes once.
const telegramAskPollInterval = 3 * time.Second

// telegramAskGrace is how long past the question's own expiry the CLI keeps
// asking. Small, and only so the server — which owns the real deadline — is
// what normally ends the wait.
const telegramAskGrace = 30 * time.Second

// telegramAskMaxFailures bounds consecutive unreadable polls. A question that
// cannot be read will not become readable by asking again.
const telegramAskMaxFailures = 5

// askTimeout mirrors the server's default and cap, so the local deadline does
// not end the wait before the question itself does.
func askTimeout(seconds int) time.Duration {
	const (
		def = 10 * time.Minute
		max = 60 * time.Minute
	)
	if seconds <= 0 {
		return def
	}
	if d := time.Duration(seconds) * time.Second; d < max {
		return d
	}
	return max
}

func telegramAskPollErrorIsPermanent(err error) bool {
	var httpErr *cli.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode >= http.StatusBadRequest &&
		httpErr.StatusCode < http.StatusInternalServerError &&
		httpErr.StatusCode != http.StatusTooManyRequests
}

var telegramAskCmd = &cobra.Command{
	Use:   "ask [question]",
	Short: "Ask the group a question and wait for someone to choose",
	Long: "Post a question with buttons and block until someone taps one.\n\n" +
		"Use this for a decision only a human should make — deploying, merging,\n" +
		"anything with a blast radius. Prints the chosen option and exits 0. On\n" +
		"timeout it prints nothing and exits non-zero, which is the signal to\n" +
		"stop rather than to guess.\n\n" +
		"The first tap wins, and only someone allowed to instruct this agent can\n" +
		"answer.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := strings.TrimSpace(strings.Join(args, " "))
		if prompt == "" {
			return fmt.Errorf("nothing to ask: pass the question as an argument")
		}
		if len(telegramAskOptions) < 2 {
			return fmt.Errorf("at least two --option values are required")
		}

		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		body := map[string]any{"prompt": prompt, "options": telegramAskOptions}
		if strings.TrimSpace(telegramAskChat) != "" {
			body["chat_id"] = strings.TrimSpace(telegramAskChat)
		}
		if telegramAskTimeout > 0 {
			body["timeout_seconds"] = telegramAskTimeout
		}
		var asked struct {
			QuestionID string `json:"question_id"`
			ChatID     string `json:"chat_id"`
		}
		if err := client.PostJSON(cmd.Context(), "/api/agents/me/telegram/ask", body, &asked); err != nil {
			return err
		}
		// Progress goes to stderr so stdout carries only the answer — the
		// caller is a script that will read it.
		fmt.Fprintf(os.Stderr, "Asked in %s, waiting for an answer...\n", asked.ChatID)

		// A local deadline as well as the server-side expiry. The loop learns
		// that a question expired by READING it, so a poll that can never
		// succeed — revoked task token, deleted installation — would otherwise
		// spin until the whole command is killed, with the agent blocked behind
		// it. Slack over the requested timeout so the server's own expiry is
		// what normally ends this.
		deadline := time.Now().Add(askTimeout(telegramAskTimeout) + telegramAskGrace)
		path := "/api/agents/me/telegram/questions/" + asked.QuestionID
		consecutiveFailures := 0
		for {
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-time.After(telegramAskPollInterval):
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("gave up waiting for an answer")
			}
			var state struct {
				Status string `json:"status"`
				Answer string `json:"answer"`
			}
			if err := client.GetJSON(cmd.Context(), path, &state); err != nil {
				// A transient failure must not be read as "no answer". A
				// permanent one must not be read as transient either: if the
				// question cannot be read at all, no amount of retrying will
				// produce a decision.
				if telegramAskPollErrorIsPermanent(err) {
					return fmt.Errorf("question can no longer be read: %w", err)
				}
				consecutiveFailures++
				if consecutiveFailures >= telegramAskMaxFailures {
					return fmt.Errorf("could not read the question after %d attempts: %w",
						consecutiveFailures, err)
				}
				fmt.Fprintf(os.Stderr, "poll failed, retrying: %v\n", err)
				continue
			}
			consecutiveFailures = 0
			switch state.Status {
			case "answered":
				fmt.Fprintln(os.Stdout, state.Answer)
				return nil
			case "expired":
				return fmt.Errorf("nobody answered in time")
			}
		}
	},
}

func init() {
	telegramSendCmd.Flags().StringVar(&telegramSendChat, "chat", "",
		"Numeric chat id (default: the agent's default chat)")
	telegramSendCmd.Flags().StringVar(&telegramSendText, "text", "",
		"Message text (alternative to a positional argument)")
	telegramSendCmd.Flags().BoolVar(&telegramSendStdin, "stdin", false,
		"Read the message from stdin")

	telegramAskCmd.Flags().StringVar(&telegramAskChat, "chat", "",
		"Numeric chat id (default: the agent's default chat)")
	telegramAskCmd.Flags().StringArrayVar(&telegramAskOptions, "option", nil,
		"An answer button; repeat for each choice (at least two)")
	telegramAskCmd.Flags().IntVar(&telegramAskTimeout, "timeout", 0,
		"Seconds to wait before giving up (default 600, max 3600)")

	telegramCmd.AddCommand(telegramChatsCmd)
	telegramCmd.AddCommand(telegramSendCmd)
	telegramCmd.AddCommand(telegramAskCmd)
	rootCmd.AddCommand(telegramCmd)
}
