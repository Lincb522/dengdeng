package service

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"dengdeng/internal/config"
)

var ErrSMTPNotConfigured = errors.New("email verification service is not configured")

// RegistrationMailer is intentionally small so registration can be exercised
// in tests without connecting to a real SMTP server.
type RegistrationMailer interface {
	Configured() bool
	SendRegistrationCode(to, code string) error
}

type SMTPMailer struct {
	cfg           config.SMTPConfig
	siteName      string
	sitePublicURL string
}

func NewSMTPMailer(cfg config.SMTPConfig, siteName, sitePublicURL string) *SMTPMailer {
	return &SMTPMailer{
		cfg:           cfg,
		siteName:      siteName,
		sitePublicURL: strings.TrimRight(strings.TrimSpace(sitePublicURL), "/"),
	}
}

func (m *SMTPMailer) Configured() bool {
	return m.cfg.Host != "" && m.cfg.Port > 0 && m.cfg.User != "" && m.cfg.Pass != ""
}

func (m *SMTPMailer) SendRegistrationCode(to, code string) error {
	if !m.Configured() {
		return ErrSMTPNotConfigured
	}
	from := m.cfg.From
	if from == "" {
		from = m.cfg.User
	}
	message := registrationEmail(m.siteName, m.sitePublicURL, from, to, code)
	return m.send(from, to, message)
}

// SendOperationalAlert reuses the verified SMTP transport for a compact,
// credential-free alert. It is invoked only when a new incident opens, never
// once per repeated health check.
func (m *SMTPMailer) SendOperationalAlert(to, title, summary string) error {
	if !m.Configured() {
		return ErrSMTPNotConfigured
	}
	from := m.cfg.From
	if from == "" {
		from = m.cfg.User
	}
	site := html.EscapeString(strings.TrimSpace(m.siteName))
	if site == "" {
		site = "DengDeng AI"
	}
	title = html.EscapeString(strings.TrimSpace(title))
	summary = html.EscapeString(strings.TrimSpace(summary))
	subject := mime.QEncoding.Encode("UTF-8", fmt.Sprintf("【%s】%s", site, title))
	fromName := mime.QEncoding.Encode("UTF-8", site)
	mark := emailBrandMark(m.sitePublicURL, site)
	message := fmt.Sprintf("MIME-Version: 1.0\r\nFrom: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n<!doctype html><html lang=\"zh-CN\"><body style=\"margin:0;background:#fffaf1;color:#30261e;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif\"><table role=\"presentation\" width=\"100%%\" cellspacing=\"0\" cellpadding=\"0\" style=\"padding:32px 12px\"><tr><td align=\"center\"><table role=\"presentation\" width=\"100%%\" cellspacing=\"0\" cellpadding=\"0\" style=\"max-width:520px;background:#fffdf8;border:1px solid #e8d9c6;border-radius:14px;overflow:hidden\"><tr><td style=\"height:5px;background:#c98a20\"></td></tr><tr><td style=\"padding:30px 34px\"><table role=\"presentation\" cellspacing=\"0\" cellpadding=\"0\"><tr><td style=\"padding-right:10px\">%s</td><td style=\"font-size:14px;font-weight:700;letter-spacing:.04em\">%s · 运行告警</td></tr></table><h1 style=\"margin:24px 0 10px;font-size:23px;line-height:1.3\">%s</h1><p style=\"margin:0;color:#746154;font-size:14px;line-height:1.75;white-space:pre-wrap\">%s</p><p style=\"margin:22px 0 0;color:#9a8065;font-size:12px;line-height:1.6\">请前往管理端的「告警与巡检」确认处理情况。</p></td></tr></table></td></tr></table></body></html>", fromName, from, to, subject, mark, site, title, summary)
	return m.send(from, to, message)
}

func (m *SMTPMailer) send(from, to, message string) error {
	address := net.JoinHostPort(m.cfg.Host, fmt.Sprintf("%d", m.cfg.Port))
	var (
		client *smtp.Client
		err    error
	)
	if m.cfg.Secure {
		dialer := &net.Dialer{Timeout: 15 * time.Second}
		conn, dialErr := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName: m.cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
		if dialErr != nil {
			return fmt.Errorf("connect smtp: %w", dialErr)
		}
		client, err = smtp.NewClient(conn, m.cfg.Host)
	} else {
		client, err = smtp.Dial(address)
		if err == nil {
			if ok, _ := client.Extension("STARTTLS"); ok {
				err = client.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12})
			}
		}
	}
	if err != nil {
		return fmt.Errorf("connect smtp: %w", err)
	}
	defer client.Quit()

	auth := smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("authenticate smtp: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("set sender: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("set recipient: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("open email body: %w", err)
	}
	if _, err := w.Write([]byte(message)); err != nil {
		return fmt.Errorf("write email body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

// emailBrandMark uses the deployed PNG app icon as an image avatar. It stays
// a normal HTTPS resource instead of a data URI, which is more broadly shown
// by Gmail, Apple Mail and Outlook. The dark D fallback remains useful for
// local development where SITE_PUBLIC_URL is intentionally unset.
func emailBrandMark(sitePublicURL, siteName string) string {
	base := strings.TrimRight(strings.TrimSpace(sitePublicURL), "/")
	if base == "" {
		return `<span style="display:inline-block;width:42px;height:42px;border-radius:12px;background:#0B0E14;color:#FFB224;font-size:22px;font-weight:800;line-height:42px;text-align:center">D</span>`
	}
	return fmt.Sprintf(`<img src="%s/brand/dengdeng-avatar.png" width="42" height="42" alt="%s" style="display:block;width:42px;height:42px;border:0;border-radius:12px;outline:none;text-decoration:none" />`, html.EscapeString(base), html.EscapeString(siteName))
}

// registrationEmail uses conservative table markup and inline styles so the
// warm interface palette holds up in Gmail, Apple Mail, and Outlook.
func registrationEmail(siteName, sitePublicURL, from, to, code string) string {
	site := html.EscapeString(strings.TrimSpace(siteName))
	if site == "" {
		site = "DengDeng AI"
	}
	fromName := mime.QEncoding.Encode("UTF-8", site)
	subject := mime.QEncoding.Encode("UTF-8", fmt.Sprintf("【%s】邮箱验证码", site))
	mark := emailBrandMark(sitePublicURL, site)
	return fmt.Sprintf("MIME-Version: 1.0\r\nFrom: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n<!doctype html><html lang=\"zh-CN\"><body style=\"margin:0;background:#fffaf1;color:#30261e;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;\"><table role=\"presentation\" width=\"100%%\" cellspacing=\"0\" cellpadding=\"0\" style=\"background:#fffaf1;padding:32px 12px;\"><tr><td align=\"center\"><table role=\"presentation\" width=\"100%%\" cellspacing=\"0\" cellpadding=\"0\" style=\"max-width:520px;background:#fffdf8;border:1px solid #e8d9c6;border-radius:14px;overflow:hidden;\"><tr><td style=\"height:5px;background:#c98a20;\"></td></tr><tr><td style=\"padding:32px 34px 28px;\"><table role=\"presentation\" cellspacing=\"0\" cellpadding=\"0\"><tr><td style=\"padding-right:10px\">%s</td><td style=\"font-size:14px;font-weight:700;letter-spacing:.04em;color:#30261e;\">%s</td></tr></table><h1 style=\"margin:28px 0 10px;font-size:25px;line-height:1.25;color:#30261e;\">确认你的邮箱</h1><p style=\"margin:0;color:#746154;font-size:15px;line-height:1.7;\">输入下面的验证码，完成账户注册。</p><div style=\"margin:26px 0 20px;padding:18px 20px;border-radius:10px;background:#30261e;color:#fffdf8;font-size:29px;font-weight:750;letter-spacing:9px;line-height:1;text-align:center;\">%s</div><p style=\"margin:0;color:#746154;font-size:13px;line-height:1.7;\">验证码 10 分钟内有效。如非本人操作，无需处理这封邮件。</p></td></tr><tr><td style=\"padding:17px 34px;border-top:1px solid #e8d9c6;color:#9a8065;font-size:12px;line-height:1.6;\">此邮件由 %s 自动发送，请勿直接回复。</td></tr></table></td></tr></table></body></html>", fromName, from, to, subject, mark, site, html.EscapeString(code), site)
}
