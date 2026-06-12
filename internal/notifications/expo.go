package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const defaultPushURL = "https://exp.host/--/api/v2/push/send"

// ExpoClient sends push notifications via the Expo Push API.
type ExpoClient struct {
	PushURL     string
	ReceiptsURL string
	AccessToken string
	HTTP        *http.Client
}

func NewExpoClient(pushURL, accessToken string) *ExpoClient {
	if pushURL == "" {
		pushURL = defaultPushURL
	}
	receiptsURL := strings.Replace(pushURL, "/push/send", "/push/getReceipts", 1)
	return &ExpoClient{
		PushURL:     pushURL,
		ReceiptsURL: receiptsURL,
		AccessToken: accessToken,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
	}
}

type pushMessage struct {
	To               string         `json:"to"`
	Title            string         `json:"title,omitempty"`
	Body             string         `json:"body,omitempty"`
	Data             map[string]any `json:"data,omitempty"`
	ContentAvailable *bool          `json:"_contentAvailable,omitempty"`
	Priority         string         `json:"priority,omitempty"`
}

type pushTicket struct {
	Status string `json:"status"`
	ID     string `json:"id"`
}

type pushSendResponse struct {
	Data []pushTicket `json:"data"`
}

type receiptResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Details struct {
		Error string `json:"error"`
	} `json:"details"`
}

type receiptsResponse struct {
	Data map[string]receiptResult `json:"data"`
}

// SendResult maps each expo push token to its ticket id (empty on send error).
type SendResult struct {
	TicketByToken map[string]string
}

// Send posts a batch of messages and returns ticket ids for ok responses.
func (c *ExpoClient) Send(ctx context.Context, msgs []pushMessage) (SendResult, error) {
	if len(msgs) == 0 {
		return SendResult{}, nil
	}
	body, err := json.Marshal(msgs)
	if err != nil {
		return SendResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.PushURL, bytes.NewReader(body))
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return SendResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return SendResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return SendResult{}, fmt.Errorf("expo push: status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed pushSendResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return SendResult{}, err
	}

	result := SendResult{TicketByToken: make(map[string]string, len(parsed.Data))}
	for i, ticket := range parsed.Data {
		if i >= len(msgs) {
			break
		}
		if ticket.Status == "ok" && ticket.ID != "" {
			result.TicketByToken[msgs[i].To] = ticket.ID
		} else {
			slog.WarnContext(ctx, "expo: push ticket error",
				"token", msgs[i].To, "status", ticket.Status)
		}
	}
	return result, nil
}

// CheckReceipts polls Expo for delivery receipts. Returns expo tokens that should
// be pruned (DeviceNotRegistered).
func (c *ExpoClient) CheckReceipts(ctx context.Context, ticketIDs []string) ([]string, error) {
	if len(ticketIDs) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(map[string][]string{"ids": ticketIDs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ReceiptsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expo receipts: status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed receiptsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}

	var dead []string
	for id, receipt := range parsed.Data {
		if receipt.Status == "error" && receipt.Details.Error == "DeviceNotRegistered" {
			dead = append(dead, id)
		} else if receipt.Status == "error" {
			slog.WarnContext(ctx, "expo: receipt error", "ticket", id, "message", receipt.Message)
		}
	}
	return dead, nil
}
