package policy

type DailyUsageCountRow struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
	Day    string `json:"day"`
	Count  int64  `json:"count"`
}
