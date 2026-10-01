package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"registration.local/frontend/internal/resources"
	"registration.local/frontend/internal/tgfmt"
)

type Client struct {
	Base string
	HTTP *http.Client
}
type APIError struct {
	Code       int
	RetryAfter int64
	Uncertain  bool
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram request failed (code=%d)", e.Code) }
func New(token string) *Client {
	return &Client{Base: "https://api.telegram.org/bot" + token + "/", HTTP: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) request(ctx context.Context, method, contentType string, body io.Reader, result any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.Base+method, body)
	if err != nil {
		return errors.New("invalid Telegram endpoint")
	}
	req.Header.Set("Content-Type", contentType)
	response, err := c.HTTP.Do(req)
	if err != nil {
		return &APIError{Uncertain: true}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 {
		return &APIError{Code: response.StatusCode, Uncertain: true}
	}
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return &APIError{Code: response.StatusCode, Uncertain: true}
	}
	if !envelope.OK {
		return &APIError{Code: envelope.Code, RetryAfter: envelope.Parameters.RetryAfter}
	}
	if result != nil && json.Unmarshal(envelope.Result, result) != nil {
		return &APIError{Uncertain: true}
	}
	return nil
}
func (c *Client) Call(ctx context.Context, method string, input, result any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("invalid Telegram request")
	}
	return c.request(ctx, method, "application/json", bytes.NewReader(body), result)
}

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}
type Message struct {
	ID      int64  `json:"message_id"`
	From    User   `json:"from"`
	Chat    Chat   `json:"chat"`
	Text    string `json:"text"`
	Contact *struct {
		Phone string `json:"phone_number"`
		Owner int64  `json:"user_id"`
	} `json:"contact"`
}
type Membership struct {
	Chat Chat `json:"chat"`
	From User `json:"from"`
	New  struct {
		Status   string `json:"status"`
		IsMember bool   `json:"is_member"`
		User     User   `json:"user"`
	} `json:"new_chat_member"`
}
type Update struct {
	ID       int64    `json:"update_id"`
	Message  *Message `json:"message"`
	Callback *struct {
		ID      string   `json:"id"`
		From    User     `json:"from"`
		Data    string   `json:"data"`
		Message *Message `json:"message"`
	} `json:"callback_query"`
	Membership    *Membership `json:"chat_member"`
	BotMembership *Membership `json:"my_chat_member"`
}

func (c *Client) Send(ctx context.Context, chat int64, text tgfmt.HTML, markup resources.Markup) (int64, error) {
	var result Message
	err := c.Call(ctx, "sendMessage", struct {
		Chat   int64            `json:"chat_id"`
		Text   string           `json:"text"`
		Parse  string           `json:"parse_mode"`
		Markup resources.Markup `json:"reply_markup"`
	}{chat, string(text), "HTML", markup}, &result)
	return result.ID, err
}
func (c *Client) Copy(ctx context.Context, chat, source, message int64) (int64, error) {
	var result Message
	err := c.Call(ctx, "copyMessage", struct {
		Chat    int64 `json:"chat_id"`
		Source  int64 `json:"from_chat_id"`
		Message int64 `json:"message_id"`
	}{chat, source, message}, &result)
	return result.ID, err
}
func (c *Client) Document(ctx context.Context, chat int64, data []byte) (int64, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("chat_id", strconv.FormatInt(chat, 10)); err != nil {
		return 0, err
	}
	part, err := form.CreateFormFile("document", "registrations.xlsx")
	if err != nil {
		return 0, err
	}
	if _, err = part.Write(data); err != nil {
		return 0, err
	}
	if err = form.Close(); err != nil {
		return 0, err
	}
	var result Message
	err = c.request(ctx, "sendDocument", form.FormDataContentType(), &body, &result)
	return result.ID, err
}
func Active(status string, isMember bool) bool {
	return status == "member" || status == "administrator" || status == "creator" || (status == "restricted" && isMember)
}
