package notification

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"coinsphere/backend/plugin/official/internal/safehttp"
	"coinsphere/backend/plugin/sdk"
)

type smtpNotificationConfig struct {
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Security   string   `json:"security"`
	Username   string   `json:"username"`
	FromEmail  string   `json:"fromEmail"`
	FromName   string   `json:"fromName"`
	Recipients []string `json:"recipients"`
	Password   string
}

func (n *notificationRuntime) sendSMTP(ctx context.Context, request sdk.ActionRequest, title, message string) (string, error) {
	var config smtpNotificationConfig
	if json.Unmarshal(request.Config, &config) != nil {
		return "configuration", errors.New("invalid SMTP configuration")
	}
	password, err := request.Secrets.Read(ctx, "password")
	config.Host = strings.TrimSpace(config.Host)
	config.Username = strings.TrimSpace(config.Username)
	config.FromEmail = strings.TrimSpace(config.FromEmail)
	config.FromName = strings.TrimSpace(config.FromName)
	config.Password = string(password)
	host, validHost := safehttp.NormalizeDomain(config.Host)
	if err != nil || !validHost || config.Port < 1 || config.Port > 65535 ||
		config.Security != "implicit_tls" && config.Security != "starttls" ||
		config.Username == "" || config.Password == "" || len(config.Recipients) == 0 || len(config.Recipients) > 100 {
		return "configuration", errors.New("SMTP credentials or settings are invalid")
	}
	config.Host = host
	from, err := mail.ParseAddress(config.FromEmail)
	if err != nil || from.Address != config.FromEmail {
		return "configuration", errors.New("SMTP sender address is invalid")
	}
	recipients := make([]string, 0, len(config.Recipients))
	seen := map[string]struct{}{}
	for _, raw := range config.Recipients {
		address, parseErr := mail.ParseAddress(strings.TrimSpace(raw))
		if parseErr != nil || address.Address != strings.TrimSpace(raw) {
			return "configuration", errors.New("SMTP recipient address is invalid")
		}
		if _, exists := seen[address.Address]; !exists {
			seen[address.Address] = struct{}{}
			recipients = append(recipients, address.Address)
		}
	}
	sort.Strings(recipients)
	addresses, err := n.http.ResolvePublicDomain(ctx, config.Host)
	if err != nil {
		return notificationRequestCategory(ctx, err), err
	}
	connection, err := dialSMTPPublic(ctx, addresses, config.Port)
	if err != nil {
		return notificationRequestCategory(ctx, err), err
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancel()
	defer connection.Close()
	deadline := time.Now().Add(notificationTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = connection.SetDeadline(deadline)
	tlsConfig := &tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}
	if config.Security == "implicit_tls" {
		tlsConnection := tls.Client(connection, tlsConfig)
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return "tls", err
		}
		connection = tlsConnection
	}
	client, err := smtp.NewClient(connection, config.Host)
	if err != nil {
		return "protocol", err
	}
	defer client.Close()
	if config.Security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return "tls", errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return "tls", err
		}
	}
	if err := client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host)); err != nil {
		return "authentication", err
	}
	if err := client.Mail(config.FromEmail); err != nil {
		return "provider_rejected", err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return "provider_rejected", err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return "provider_rejected", err
	}
	fromHeader := (&mail.Address{Name: config.FromName, Address: config.FromEmail}).String()
	mailBody := strings.Join([]string{
		"From: " + fromHeader,
		"To: " + strings.Join(recipients, ", "),
		"Subject: " + mime.QEncoding.Encode("UTF-8", title),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		message,
	}, "\r\n")
	if _, err := writer.Write([]byte(mailBody)); err != nil {
		_ = writer.Close()
		return "network", err
	}
	if err := writer.Close(); err != nil {
		return "provider_rejected", err
	}
	if err := client.Quit(); err != nil {
		return "network", err
	}
	return "", nil
}

func dialSMTPPublic(ctx context.Context, addresses []netip.Addr, port int) (net.Conn, error) {
	var lastErr error
	dialer := net.Dialer{Timeout: notificationTimeout}
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address.String(), fmt.Sprint(port)))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("SMTP host has no public addresses")
}
