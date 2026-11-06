package library

import "context"

var ErrSetup = &SourceError{Code: "setup", Message: "connection needs configuration"}

type SourceError struct{ Code, Message string }

func (e *SourceError) Error() string { return e.Message }
func (e *SourceError) Is(target error) bool {
	other, ok := target.(*SourceError)
	return ok && e.Code == other.Code
}

type Setting struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Required bool   `json:"required,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
}
type NetworkAccess interface{ NetworkPermissions() []string }

type Configurable interface {
	Settings() []Setting
	Text(string, string) string
}
type CredentialWriter interface {
	Credentials
	Store(context.Context, string, map[string]string) error
	Delete(context.Context, string) error
}
type Authenticator interface {
	Authenticate(context.Context, Source, map[string]string) (map[string]string, error)
}

// Preparer allocates a real playback session. Resolve remains safe for artwork,
// probing and queue restoration and must not start server-side playback work.
type PlayRequest struct {
	Reference
	Position float64
}
type Preparer interface {
	Prepare(context.Context, PlayRequest) (Playback, error)
}

type PlaybackEvent struct {
	Type     string  `json:"type"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Paused   bool    `json:"paused"`
	Ended    bool    `json:"ended"`
	Failed   bool    `json:"failed"`
}
type PlaybackObserver interface {
	Report(context.Context, Source, Reference, Playback, PlaybackEvent, Host) error
}
