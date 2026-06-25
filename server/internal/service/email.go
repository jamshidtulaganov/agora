package service

import (
	"crypto/tls"
	"fmt"
	"html"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/resend/resend-go/v2"
)

// maxSubjectFieldRunes bounds how much user-controlled text (workspace name,
// inviter name) can land in an email Subject. Prevents attackers from stuffing
// a full phishing pitch into a workspace name that gets sent from our domain.
const maxSubjectFieldRunes = 60

type EmailService struct {
	client          *resend.Client
	fromEmail       string
	smtpHost        string
	smtpPort        string
	smtpUsername    string
	smtpPassword    string
	smtpTLSInsecure bool
	smtpTLSImplicit bool
	smtpEHLOName    string
}

func NewEmailService() *EmailService {
	apiKey := os.Getenv("RESEND_API_KEY")
	from := strings.TrimSpace(os.Getenv("RESEND_FROM_EMAIL"))
	if from == "" {
		from = "noreply@agora.dev"
	}

	smtpHost := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	smtpPort := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if smtpPort == "" {
		smtpPort = "25"
	}
	smtpUsername := os.Getenv("SMTP_USERNAME")
	smtpPassword := os.Getenv("SMTP_PASSWORD")
	smtpTLSInsecure := os.Getenv("SMTP_TLS_INSECURE") == "true"

	// EHLO/HELO name, only relevant on the SMTP relay send path. net/smtp defaults
	// to "localhost", which strict relays (e.g. smtp-relay.gmail.com) reject from a
	// public source. Fall back to the machine hostname when SMTP_EHLO_NAME is unset.
	// Resolved only in SMTP mode so the Resend/DEV paths never touch os.Hostname()
	// or emit its failure log.
	var smtpEHLOName string
	if smtpHost != "" {
		smtpEHLOName = strings.TrimSpace(os.Getenv("SMTP_EHLO_NAME"))
		if smtpEHLOName == "" {
			hostname, hostErr := os.Hostname()
			if hostErr != nil {
				// Empty name makes sendSMTP skip Hello() and fall back to net/smtp's
				// lazy "localhost" — which strict relays reject. Surface it so operators
				// know to set SMTP_EHLO_NAME explicitly.
				fmt.Printf("EmailService: os.Hostname() failed (%v); SMTP EHLO falls back to \"localhost\" — set SMTP_EHLO_NAME for strict relays\n", hostErr)
			}
			smtpEHLOName = hostname
		}
	}

	// SMTP_TLS=implicit forces an immediate TLS handshake on connect (SMTPS).
	// Required by providers like Aliyun enterprise mail that only offer port 465
	// SSL and do not advertise STARTTLS. Default (empty / "starttls") preserves
	// the prior STARTTLS-upgrade behavior.
	smtpTLSMode := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS")))
	smtpTLSImplicit := smtpTLSMode == "implicit" || smtpTLSMode == "smtps" || smtpTLSMode == "ssl"
	if smtpTLSMode == "" && smtpPort == "465" {
		smtpTLSImplicit = true
	}
	if smtpTLSMode != "" && !smtpTLSImplicit && smtpTLSMode != "starttls" {
		fmt.Printf("EmailService: SMTP_TLS=%q not recognized, falling back to starttls\n", smtpTLSMode)
	}

	var client *resend.Client
	if apiKey != "" {
		client = resend.NewClient(apiKey)
	}

	switch {
	case smtpHost != "":
		tlsLabel := "starttls"
		if smtpTLSImplicit {
			tlsLabel = "implicit-tls"
		}
		fmt.Printf("EmailService: SMTP relay %s:%s (%s) from=%s\n", smtpHost, smtpPort, tlsLabel, from)
	case client != nil:
		fmt.Printf("EmailService: Resend API from=%s\n", from)
	default:
		fmt.Println("EmailService: DEV mode — codes printed to stdout (set AGORA_DEV_VERIFICATION_CODE in .env for a fixed local code)")
	}

	return &EmailService{
		client:          client,
		fromEmail:       from,
		smtpHost:        smtpHost,
		smtpPort:        smtpPort,
		smtpUsername:    smtpUsername,
		smtpPassword:    smtpPassword,
		smtpTLSInsecure: smtpTLSInsecure,
		smtpTLSImplicit: smtpTLSImplicit,
		smtpEHLOName:    smtpEHLOName,
	}
}

// sendSMTP delivers an HTML email via an SMTP server.
// Supports unauthenticated relay (SMTP_USERNAME empty) and authenticated SMTP.
// Upgrades to STARTTLS when advertised by the server.
// Set SMTP_TLS_INSECURE=true for self-signed or private CA certificates.
func (s *EmailService) sendSMTP(to, subject, htmlBody string) error {
	addr := net.JoinHostPort(s.smtpHost, s.smtpPort)

	tlsCfg := &tls.Config{
		ServerName:         s.smtpHost,
		InsecureSkipVerify: s.smtpTLSInsecure, //nolint:gosec // opt-in via SMTP_TLS_INSECURE=true
	}

	// Bounded dial + whole-session deadline: prevents a blackholed SMTP server
	// from hanging the auth handler (or a background goroutine) indefinitely.
	var conn net.Conn
	var err error
	if s.smtpTLSImplicit {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	if err = conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		conn.Close()
		return fmt.Errorf("smtp set deadline: %w", err)
	}

	c, err := smtp.NewClient(conn, s.smtpHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	// Greet with a real hostname before any other command, else net/smtp lazily
	// EHLOs "localhost" — which strict relays drop, surfacing as an opaque EOF on
	// a later command rather than at the EHLO itself.
	if s.smtpEHLOName != "" {
		if err = c.Hello(s.smtpEHLOName); err != nil {
			return fmt.Errorf("smtp EHLO %s: %w", s.smtpEHLOName, err)
		}
	}

	// STARTTLS upgrade only makes sense when the underlying connection is still
	// plaintext. Skip when we already dialed with implicit TLS.
	if !s.smtpTLSImplicit {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err = c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}

	if s.smtpUsername != "" {
		auth := smtp.PlainAuth("", s.smtpUsername, s.smtpPassword, s.smtpHost)
		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	// Probe 8BITMIME after (possible) STARTTLS so the extension list is current.
	// Use quoted-printable for relays that don't advertise 8BITMIME — safer for
	// non-ASCII workspace/inviter names crossing strict or older SMTP hops.
	has8Bit, _ := c.Extension("8BITMIME")
	encodedSubject := mime.QEncoding.Encode("utf-8", subject)
	msgID := fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), s.smtpHost)

	var bodyBytes []byte
	var cte string
	if has8Bit {
		bodyBytes = []byte(htmlBody)
		cte = "8bit"
	} else {
		var buf strings.Builder
		qpw := quotedprintable.NewWriter(&buf)
		_, _ = qpw.Write([]byte(htmlBody))
		_ = qpw.Close()
		bodyBytes = []byte(buf.String())
		cte = "quoted-printable"
	}

	if err = c.Mail(s.fromEmail); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err = c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO <%s>: %w", to, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	headers := "From: " + s.fromEmail + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + encodedSubject + "\r\n" +
		"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: " + msgID + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: " + cte + "\r\n" +
		"\r\n"
	if _, err = fmt.Fprintf(w, "%s%s", headers, bodyBytes); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("smtp end data: %w", err)
	}
	return c.Quit()
}

// appBaseURL returns the public app origin used to build links in emails,
// falling back to the cloud default when FRONTEND_ORIGIN is unset.
func appBaseURL() string {
	appURL := strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	if appURL == "" {
		appURL = "https://app.agora.dev"
	}
	return strings.TrimRight(appURL, "/")
}

// emailShell wraps content in a branded, email-client-safe HTML layout.
// Uses table layout + inline styles (flexbox/grid and <style> blocks are
// unreliable across Gmail/Outlook). The preheader is the muted preview line
// shown in the inbox list next to the subject. contentHTML is injected verbatim,
// so callers must HTML-escape any user-controlled values before passing them in.
func emailShell(appURL, preheader, contentHTML string) string {
	tmpl := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="color-scheme" content="light">
</head>
<body style="margin:0;padding:0;background-color:#f4f4f5;">
<span style="display:none;max-height:0;overflow:hidden;opacity:0;">{{PREHEADER}}</span>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background-color:#f4f4f5;padding:24px 12px;">
<tr><td align="center">
<table role="presentation" width="420" cellpadding="0" cellspacing="0" style="width:420px;max-width:100%;background-color:#ffffff;border:1px solid #e4e4e7;border-radius:10px;overflow:hidden;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<tr><td style="padding:18px 22px 0 22px;">
<a href="{{APP_URL}}" style="text-decoration:none;color:#2071cc;font-size:17px;font-weight:700;letter-spacing:-0.02em;">Agora</a>
</td></tr>
<tr><td style="padding:14px 22px 18px 22px;">
{{CONTENT}}
</td></tr>
<tr><td style="padding:12px 22px;border-top:1px solid #f1f1f3;">
<p style="margin:0;font-size:11px;color:#a1a1aa;line-height:1.6;">
<a href="{{APP_URL}}" style="color:#2071cc;text-decoration:none;font-weight:500;">Open Agora</a>
&nbsp;&middot;&nbsp;
<a href="{{APP_URL}}/inbox" style="color:#2071cc;text-decoration:none;font-weight:500;">Inbox</a>
&nbsp;&middot;&nbsp;
<a href="{{APP_URL}}/settings" style="color:#2071cc;text-decoration:none;font-weight:500;">Settings</a>
<br>Agora — AI-native task management.
</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`
	r := strings.NewReplacer(
		"{{APP_URL}}", html.EscapeString(appURL),
		"{{PREHEADER}}", html.EscapeString(preheader),
		"{{CONTENT}}", contentHTML,
	)
	return r.Replace(tmpl)
}

// SendVerificationCode sends a one-time login code. The code is server-generated
// (6-digit numeric) so no user-controlled text reaches the email body here.
// Delivery priority: SMTP relay → Resend API → DEV stdout.
func (s *EmailService) SendVerificationCode(to, code string) error {
	content := fmt.Sprintf(
		`<h1 style="margin:0 0 6px 0;font-size:17px;font-weight:600;color:#0a0d12;">Your verification code</h1>
		<p style="margin:0 0 14px 0;font-size:13px;color:#52525b;line-height:1.5;">Enter this code to finish signing in.</p>
		<table role="presentation" cellpadding="0" cellspacing="0" style="margin:0 0 14px 0;">
		<tr><td style="background-color:#eff6ff;border:1px solid #2071cc;border-radius:8px;padding:12px 20px;font-size:28px;font-weight:700;letter-spacing:8px;color:#0a4da3;text-align:center;">%s</td></tr>
		</table>
		<p style="margin:0 0 3px 0;font-size:13px;color:#52525b;">This code expires in 10 minutes.</p>
		<p style="margin:0;font-size:12px;color:#a1a1aa;line-height:1.5;">If you didn't request this code, you can safely ignore this email.</p>`,
		code)
	body := emailShell(appBaseURL(), "Your Agora verification code", content)

	if s.smtpHost != "" {
		return s.sendSMTP(to, "Your Agora verification code", body)
	}
	if s.client == nil {
		fmt.Printf("[DEV] Verification code for %s: %s\n", to, code)
		return nil
	}
	params := &resend.SendEmailRequest{
		From:    s.fromEmail,
		To:      []string{to},
		Subject: "Your Agora verification code",
		Html:    body,
	}
	_, err := s.client.Emails.Send(params)
	return err
}

// SendInvitationEmail notifies the invitee that they have been invited to a workspace.
// invitationID is included in the URL so the email deep-links to /invite/{id}.
func (s *EmailService) SendInvitationEmail(to, inviterName, workspaceName, invitationID string) error {
	appURL := strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	if appURL == "" {
		appURL = "https://app.agora.dev"
	}
	inviteURL := fmt.Sprintf("%s/invite/%s", appURL, invitationID)

	if s.smtpHost != "" {
		params := buildInvitationParams(s.fromEmail, to, inviterName, workspaceName, inviteURL)
		return s.sendSMTP(to, params.Subject, params.Html)
	}
	if s.client == nil {
		fmt.Printf("[DEV] Invitation email to %s: %s invited you to %s — %s\n", to, inviterName, workspaceName, inviteURL)
		return nil
	}
	params := buildInvitationParams(s.fromEmail, to, inviterName, workspaceName, inviteURL)
	_, err := s.client.Emails.Send(params)
	return err
}

// buildInvitationParams assembles the email request for an invitation.
// Separated from SendInvitationEmail so the sanitization behavior is unit-testable
// without needing to mock the Resend SDK or an SMTP server.
func buildInvitationParams(from, to, inviterName, workspaceName, inviteURL string) *resend.SendEmailRequest {
	safeWorkspace := html.EscapeString(workspaceName)
	safeInviter := html.EscapeString(inviterName)
	subjectInviter := sanitizeSubjectField(inviterName)
	subjectWorkspace := sanitizeSubjectField(workspaceName)

	content := fmt.Sprintf(
		`<h1 style="margin:0 0 6px 0;font-size:17px;font-weight:600;color:#0a0d12;">You're invited to join %s</h1>
		<p style="margin:0 0 16px 0;font-size:13px;color:#52525b;line-height:1.5;"><strong style="color:#0a0d12;">%s</strong> invited you to collaborate in the <strong style="color:#0a0d12;">%s</strong> workspace on Agora.</p>
		<table role="presentation" cellpadding="0" cellspacing="0" style="margin:0 0 16px 0;">
		<tr><td style="background-color:#2071cc;border-radius:6px;">
		<a href="%s" style="display:inline-block;padding:10px 22px;color:#ffffff;text-decoration:none;font-size:13px;font-weight:600;">Accept invitation</a>
		</td></tr>
		</table>
		<p style="margin:0;font-size:12px;color:#a1a1aa;line-height:1.5;">You'll need to log in to accept or decline the invitation.</p>`,
		safeWorkspace, safeInviter, safeWorkspace, inviteURL)

	return &resend.SendEmailRequest{
		From:    from,
		To:      []string{to},
		Subject: fmt.Sprintf("%s invited you to %s on Agora", subjectInviter, subjectWorkspace),
		Html:    emailShell(appBaseURL(), fmt.Sprintf("%s invited you to %s on Agora", safeInviter, safeWorkspace), content),
	}
}

// sanitizeSubjectField prepares user-controlled text for the email Subject line.
// Subject is not HTML-rendered, so HTML-escaping would leak literal entities
// (e.g. &lt;script&gt;) into the recipient's inbox. Instead strip control
// characters (defense in depth against header-injection-adjacent abuse even
// though Resend also filters CR/LF) and cap length so attackers can't stuff
// a full phishing subject into a workspace name.
func sanitizeSubjectField(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	cleaned := b.String()
	if utf8.RuneCountInString(cleaned) <= maxSubjectFieldRunes {
		return cleaned
	}
	runes := []rune(cleaned)
	return string(runes[:maxSubjectFieldRunes-1]) + "…"
}
