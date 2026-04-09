package notify

import (
	"fmt"
	"net/smtp"

	"github.com/TechXTT/marktbot/internal/models"
)

// EmailNotifier sends deal alert emails via SMTP.
type EmailNotifier struct {
	host string
	port string
	user string
	pass string
	from string
}

func NewEmail(host, port, user, pass, from string) *EmailNotifier {
	return &EmailNotifier{host: host, port: port, user: user, pass: pass, from: from}
}

func (n *EmailNotifier) Enabled() bool {
	return n.host != "" && n.user != ""
}

func (n *EmailNotifier) SendDealAlert(to string, listing models.Listing, score float64) error {
	auth := smtp.PlainAuth("", n.user, n.pass, n.host)
	subject := "Strong deal found: " + listing.Title
	body := buildDealEmailHTML(listing, score)
	msg := "From: markt <" + n.from + ">\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n\r\n" +
		body
	return smtp.SendMail(n.host+":"+n.port, auth, n.from, []string{to}, []byte(msg))
}

func buildDealEmailHTML(listing models.Listing, score float64) string {
	return fmt.Sprintf(`<!DOCTYPE html><html><body style="font-family:Inter,sans-serif;background:#edf2ef;padding:32px">
<div style="max-width:560px;margin:0 auto;background:#fff;border-radius:16px;overflow:hidden">
  <div style="background:#0a1410;padding:24px 28px">
    <h1 style="color:#68e2b8;margin:0;font-size:1.1rem">markt found a strong deal</h1>
  </div>
  <div style="padding:28px">
    <p style="font-size:1.1rem;font-weight:600;color:#081510;margin:0 0 8px">%s</p>
    <p style="font-size:1.4rem;font-weight:700;color:#0f8f67;margin:0 0 16px">Score: %.1f / 10</p>
    <a href="%s" style="display:inline-block;background:#0f8f67;color:#fff;padding:12px 24px;border-radius:12px;text-decoration:none;font-weight:600">View listing</a>
  </div>
</div>
</body></html>`, listing.Title, score, listing.URL)
}
