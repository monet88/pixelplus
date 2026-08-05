package application

import (
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// upstreamStopped decides whether X5 and X6 coincide. Getting it wrong is
// invisible at the client — the terminal event is identical either way — and
// only shows up as a quota/occupancy discrepancy, so it is pinned directly.
func TestUpstreamStoppedClassification(t *testing.T) {
	t.Parallel()

	failedWith := func(code domain.ErrorCode) ChatStreamTerminal {
		return ChatStreamTerminal{
			Event:       domain.ChatStreamFailed,
			FinishClass: domain.FinishFailed,
			Error:       domain.CanonicalError{Code: code},
		}
	}

	cases := []struct {
		name     string
		terminal ChatStreamTerminal
		want     bool
		why      string
	}{
		{
			name:     "completed stops",
			terminal: ChatStreamTerminal{Event: domain.ChatStreamCompleted},
			want:     true,
			why:      "a natural completion means upstream is done",
		},
		{
			name:     "canceled with confirmed stop",
			terminal: ChatStreamTerminal{Event: domain.ChatStreamCanceled, UpstreamStopConfirmed: true},
			want:     true,
			why:      "the Adapter proved the stop",
		},
		{
			name:     "canceled without confirmation survives",
			terminal: ChatStreamTerminal{Event: domain.ChatStreamCanceled, UpstreamAbortAttempted: true},
			want:     false,
			why:      "cancellation alone is never proof upstream stopped (§6.2 rule 3)",
		},
		{
			name:     "timeout survives",
			terminal: failedWith(domain.ErrCodeUpstreamTimeout),
			want:     false,
			why:      "§6.4 rule 3: a timeout follows the same residual rules as cancel; the Provider may still be generating",
		},
		{
			name:     "upstream unavailable survives",
			terminal: failedWith(domain.ErrCodeUpstreamUnavailable),
			want:     false,
			why:      "a lost transport is not proof the upstream never started",
		},
		{
			name:     "protocol drift survives",
			terminal: failedWith(domain.ErrCodeUpstreamProtocolDrift),
			want:     false,
			why:      "an unparseable response says nothing about whether generation is still running",
		},
		{
			name:     "possibly committed survives",
			terminal: failedWith(domain.ErrCodeExecutionPossiblyCommitted),
			want:     false,
			why:      "commit is explicitly uncertain",
		},
		{
			name: "timeout with a confirmed stop coincides",
			terminal: ChatStreamTerminal{
				Event:                 domain.ChatStreamFailed,
				Error:                 domain.CanonicalError{Code: domain.ErrCodeUpstreamTimeout},
				UpstreamStopConfirmed: true,
			},
			want: true,
			why:  "an observed stop overrides the conservative default",
		},
		{
			name:     "pre-upstream rejection stops",
			terminal: failedWith(domain.ErrCodeAccountNotUsable),
			want:     true,
			why:      "the Gateway rejected before upstream accepted work, so nothing can survive",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := upstreamStopped(testCase.terminal); got != testCase.want {
				t.Fatalf("upstreamStopped = %v, want %v: %s", got, testCase.want, testCase.why)
			}
		})
	}
}

// UpstreamAbortAttempted is only ever populated for `canceled` terminals, so it
// must not participate in classifying a `failed` one. This pins the regression:
// keying off the abort flag classified every failure — including a timeout the
// Gateway was required to abort — as "upstream stopped".
func TestUpstreamStoppedIgnoresAbortFlagOnFailedTerminals(t *testing.T) {
	t.Parallel()

	timeout := ChatStreamTerminal{
		Event: domain.ChatStreamFailed,
		Error: domain.CanonicalError{Code: domain.ErrCodeUpstreamTimeout},
	}
	withAbort := timeout
	withAbort.UpstreamAbortAttempted = true

	if upstreamStopped(timeout) != upstreamStopped(withAbort) {
		t.Fatalf("UpstreamAbortAttempted changed the classification of a failed terminal; only commit status may decide it")
	}
	if upstreamStopped(timeout) {
		t.Fatalf("a timeout must route to the residual protocol, not settle as upstream-stopped")
	}
}
