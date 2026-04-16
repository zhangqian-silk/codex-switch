package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

var (
	codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"
	codexTokenURL = "https://auth.openai.com/oauth/token"
)

type QuotaWindow struct {
	RemainingPercent int
	ResetAt          time.Time
}

type QuotaStatus struct {
	Hourly    QuotaWindow
	Weekly    QuotaWindow
	Plan      string
	Refreshed bool
}

type QuotaQueryOptions struct {
	Now    time.Time
	Client *http.Client
}

type usageResponse struct {
	PlanType  string         `json:"plan_type"`
	RateLimit *rateLimitInfo `json:"rate_limit"`
}

type rateLimitInfo struct {
	PrimaryWindow   *usageWindow `json:"primary_window"`
	SecondaryWindow *usageWindow `json:"secondary_window"`
}

type usageWindow struct {
	UsedPercent       *int   `json:"used_percent"`
	ResetAfterSeconds *int64 `json:"reset_after_seconds"`
	ResetAt           *int64 `json:"reset_at"`
}

type tokenRefreshResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type quotaAPIError struct {
	Detail struct {
		Code string `json:"code"`
	} `json:"detail"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
	Code string `json:"code"`
}

func QueryQuota(rawAuth []byte, opts QuotaQueryOptions) (*QuotaStatus, []byte, error) {
	auth, err := ParseAuthFile(rawAuth)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 auth.json 失败: %w", err)
	}
	if normalizeAuthMode(auth) == "apikey" {
		return nil, nil, fmt.Errorf("API Key 账号暂不支持查询 Codex OAuth 额度")
	}
	if auth.Tokens == nil || auth.Tokens.IsZero() {
		return nil, nil, fmt.Errorf("auth.json 中缺少可用 OAuth token")
	}

	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	currentAuth := *auth
	refreshed := false

	if tokenExpirySoon(currentAuth.Tokens.AccessToken, now) {
		if err := refreshTokens(&currentAuth, client, now); err != nil {
			return nil, nil, err
		}
		refreshed = true
	}

	status, err := fetchQuotaStatus(&currentAuth, client, now)
	if err != nil && shouldRetryWithRefresh(err) {
		if refreshErr := refreshTokens(&currentAuth, client, now); refreshErr == nil {
			refreshed = true
			status, err = fetchQuotaStatus(&currentAuth, client, now)
		}
	}
	if err != nil {
		return nil, nil, err
	}

	status.Refreshed = refreshed
	if !refreshed {
		return status, nil, nil
	}

	updatedRaw, err := serializeAuthFile(currentAuth)
	if err != nil {
		return nil, nil, err
	}
	return status, updatedRaw, nil
}

func fetchQuotaStatus(auth *AuthFile, client *http.Client, now time.Time) (*QuotaStatus, error) {
	req, err := http.NewRequest(http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构建额度请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(auth.Tokens.AccessToken))

	accountID := firstNonEmpty(
		auth.Tokens.AccountID,
		extractAccountIDFromJWT(auth.Tokens.AccessToken),
		extractAccountIDFromJWT(auth.Tokens.IDToken),
	)
	if accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求额度接口失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取额度响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorCode := extractQuotaErrorCode(body)
		message := fmt.Sprintf("额度接口返回错误 %d", resp.StatusCode)
		if errorCode != "" {
			message += " [error_code:" + errorCode + "]"
		}
		return nil, fmt.Errorf("%s", message)
	}

	var usage usageResponse
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, fmt.Errorf("解析额度响应失败: %w", err)
	}

	return &QuotaStatus{
		Hourly: QuotaWindow{
			RemainingPercent: remainingPercent(rateWindow(usage.RateLimit, true)),
			ResetAt:          resetTime(rateWindow(usage.RateLimit, true), now),
		},
		Weekly: QuotaWindow{
			RemainingPercent: remainingPercent(rateWindow(usage.RateLimit, false)),
			ResetAt:          resetTime(rateWindow(usage.RateLimit, false), now),
		},
		Plan: strings.TrimSpace(usage.PlanType),
	}, nil
}

func refreshTokens(auth *AuthFile, client *http.Client, now time.Time) error {
	refreshToken := strings.TrimSpace(auth.Tokens.RefreshToken)
	if refreshToken == "" {
		return fmt.Errorf("token 已失效且账号缺少 refresh_token")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", codexOAuthClientID)

	req, err := http.NewRequest(http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("构建 token 刷新请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("token 刷新请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取 token 刷新响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("token 刷新失败: status=%d", resp.StatusCode)
	}

	var payload tokenRefreshResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("解析 token 刷新响应失败: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return fmt.Errorf("token 刷新响应缺少 access_token")
	}

	auth.Tokens.AccessToken = strings.TrimSpace(payload.AccessToken)
	if strings.TrimSpace(payload.IDToken) != "" {
		auth.Tokens.IDToken = strings.TrimSpace(payload.IDToken)
	}
	if strings.TrimSpace(payload.RefreshToken) != "" {
		auth.Tokens.RefreshToken = strings.TrimSpace(payload.RefreshToken)
	}
	auth.LastRefresh = now.Format(time.RFC3339Nano)

	claims, _ := decodeJWTClaims(auth.Tokens.IDToken)
	meta, _ := decodeAuthClaims(claims)
	if meta != nil {
		auth.Tokens.AccountID = firstNonEmpty(auth.Tokens.AccountID, meta.ChatGPTAccID, meta.AccountID)
	}
	return nil
}

func serializeAuthFile(auth AuthFile) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(auth); err != nil {
		return nil, fmt.Errorf("序列化更新后的 auth.json 失败: %w", err)
	}
	return bytes.TrimSpace(buffer.Bytes()), nil
}

func tokenExpirySoon(accessToken string, now time.Time) bool {
	expiry := TokenExpiry(accessToken)
	if expiry.IsZero() {
		return false
	}
	return expiry.Before(now.Add(5 * time.Minute))
}

func shouldRetryWithRefresh(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "401") ||
		strings.Contains(lower, "token_invalidated") ||
		strings.Contains(lower, "invalid_token") ||
		strings.Contains(lower, "unauthorized")
}

func extractQuotaErrorCode(body []byte) string {
	var payload quotaAPIError
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return firstNonEmpty(payload.Detail.Code, payload.Error.Code, payload.Code)
}

func extractAccountIDFromJWT(token string) string {
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return ""
	}
	meta, err := decodeAuthClaims(claims)
	if err != nil || meta == nil {
		return ""
	}
	return firstNonEmpty(meta.ChatGPTAccID, meta.AccountID)
}

func rateWindow(limit *rateLimitInfo, primary bool) *usageWindow {
	if limit == nil {
		return nil
	}
	if primary {
		return limit.PrimaryWindow
	}
	return limit.SecondaryWindow
}

func remainingPercent(window *usageWindow) int {
	if window == nil || window.UsedPercent == nil {
		return 100
	}
	used := *window.UsedPercent
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return 100 - used
}

func resetTime(window *usageWindow, now time.Time) time.Time {
	if window == nil {
		return time.Time{}
	}
	if window.ResetAt != nil && *window.ResetAt > 0 {
		return time.Unix(*window.ResetAt, 0).UTC()
	}
	if window.ResetAfterSeconds != nil && *window.ResetAfterSeconds >= 0 {
		return now.Add(time.Duration(*window.ResetAfterSeconds) * time.Second).UTC()
	}
	return time.Time{}
}

func SetQuotaUsageURLForTest(url string) func() {
	original := codexUsageURL
	codexUsageURL = url
	return func() { codexUsageURL = original }
}

func SetQuotaTokenURLForTest(url string) func() {
	original := codexTokenURL
	codexTokenURL = url
	return func() { codexTokenURL = original }
}
