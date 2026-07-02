package zohocrm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// CreateRecord inserts one record into a module and returns the new record
// id. Zoho wraps writes in {"data":[...]} and reports per-record status
// inside a 2xx envelope, so success is checked per record, not per response.
func (c *Client) CreateRecord(ctx context.Context, module string, fields map[string]any) (string, error) {
	var out struct {
		Data []struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details struct {
				ID string `json:"id"`
			} `json:"details"`
		} `json:"data"`
	}
	payload := map[string]any{"data": []map[string]any{fields}}
	if err := c.doJSON(ctx, http.MethodPost, "/crm/v8/"+url.PathEscape(module), payload, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", fmt.Errorf("zohocrm: create %s: empty response", module)
	}
	if out.Data[0].Status != "success" {
		return "", fmt.Errorf("zohocrm: create %s: %s", module, out.Data[0].Message)
	}
	return out.Data[0].Details.ID, nil
}
