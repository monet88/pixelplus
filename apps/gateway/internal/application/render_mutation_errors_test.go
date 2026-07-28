package application_test

// Durable mutation error propagation is covered by integration of fenced store
// tests and ExecuteJob returning non-stale-fence errors. This package cannot
// import infrastructure; persistence.TestStaleFence and contract execution
// paths exercise the boundary. A compile-time check ensures domain.RenderOutcome
// has no Outputs byte field.
import (
	"reflect"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

func TestRenderOutcomeHasNoOutputBytesField(t *testing.T) {
	t.Parallel()
	if _, ok := reflect.TypeOf(domain.RenderOutcome{}).FieldByName("Outputs"); ok {
		t.Fatal("domain.RenderOutcome must not carry Outputs [][]byte; stage via RenderCaptureSink")
	}
	if _, ok := reflect.TypeOf(domain.RenderOutcome{}).FieldByName("Manifest"); !ok {
		t.Fatal("domain.RenderOutcome must carry safe Manifest metadata")
	}
}
