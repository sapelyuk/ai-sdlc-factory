package agent

import (
	"reflect"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

func TestFilterReadOnlyTools_RemovesWriteTools(t *testing.T) {
	tools := []client.ToolDef{
		{Name: "fs.read"},
		{Name: "git.push"},
		{Name: "kb.search"},
	}

	got := FilterReadOnlyTools(tools)
	want := []client.ToolDef{{Name: "fs.read"}, {Name: "kb.search"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterReadOnlyTools() = %#v, want %#v", got, want)
	}
}

func TestFilterReadOnlyTools_EmptyInput(t *testing.T) {
	if got := FilterReadOnlyTools(nil); len(got) != 0 {
		t.Fatalf("FilterReadOnlyTools(nil) = %#v, want empty", got)
	}
}
