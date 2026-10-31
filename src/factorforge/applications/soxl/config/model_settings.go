package config

// ModelSettings is how the application calls a model through the model access
// module's JSONL protocol. It is assembled from a role profile and holds no
// credentials.
type ModelSettings struct {
	Enabled        bool
	BunPath        string
	Provider       string
	AccountID      int64
	Model          string
	TimeoutSeconds int64
	CleanupSeconds int64
	MaxEvents      int
	MaxQuoteBytes  int
	MaxInputBytes  int
	MaxOutputBytes int
	MaxLineBytes   int
}
