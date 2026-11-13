package activity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeDaySpendReader returns a fixed spend and records the workspace it was
// asked about.
type fakeDaySpendReader struct {
	spent       float64
	err         error
	calls       int
	workspaceID uuid.UUID
}

func (r *fakeDaySpendReader) DaySpend(_ context.Context, workspaceID uuid.UUID) (float64, error) {
	r.calls++
	r.workspaceID = workspaceID
	return r.spent, r.err
}

func TestBudgetDailyCheckActivityUnderLimit(t *testing.T) {
	reader := &fakeDaySpendReader{spent: 1.5}
	a := NewBudgetDailyCheckActivity(reader)
	ws := uuid.New()

	out, err := a.Execute(context.Background(), BudgetDailyCheckInput{
		WorkspaceID: ws,
		MaxDailyUSD: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SpentToday != 1.5 {
		t.Errorf("spent_today = %v, want 1.5", out.SpentToday)
	}
	if out.Exceeded {
		t.Error("expected not exceeded")
	}
	if out.Warning80 {
		t.Error("expected no 80% warning")
	}
	if reader.workspaceID != ws {
		t.Errorf("reader workspace = %s, want %s", reader.workspaceID, ws)
	}
}

func TestBudgetDailyCheckActivityWarning80(t *testing.T) {
	a := NewBudgetDailyCheckActivity(&fakeDaySpendReader{spent: 8.5})

	out, err := a.Execute(context.Background(), BudgetDailyCheckInput{
		WorkspaceID: uuid.New(),
		MaxDailyUSD: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Warning80 {
		t.Errorf("expected 80%% warning, got %+v", out)
	}
	if out.Exceeded {
		t.Errorf("expected not exceeded, got %+v", out)
	}
}

func TestBudgetDailyCheckActivityExceeded(t *testing.T) {
	// Reaching the day budget is a hard stop, so the warning flag is not set:
	// the caller aborts with budget_exceeded instead.
	a := NewBudgetDailyCheckActivity(&fakeDaySpendReader{spent: 10})

	out, err := a.Execute(context.Background(), BudgetDailyCheckInput{
		WorkspaceID: uuid.New(),
		MaxDailyUSD: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Exceeded {
		t.Errorf("expected exceeded at the limit, got %+v", out)
	}
	if out.Warning80 {
		t.Errorf("expected no 80%% warning when exceeded, got %+v", out)
	}
}

func TestBudgetDailyCheckActivityDisabledBudgetSkipsReader(t *testing.T) {
	reader := &fakeDaySpendReader{spent: 100}
	a := NewBudgetDailyCheckActivity(reader)

	out, err := a.Execute(context.Background(), BudgetDailyCheckInput{
		WorkspaceID: uuid.New(),
		MaxDailyUSD: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Exceeded || out.Warning80 || out.SpentToday != 0 {
		t.Errorf("expected zero verdict, got %+v", out)
	}
	if reader.calls != 0 {
		t.Errorf("reader calls = %d, want 0", reader.calls)
	}
}

func TestBudgetDailyCheckActivityNilReaderReportsWithinBudget(t *testing.T) {
	a := NewBudgetDailyCheckActivity(nil)

	out, err := a.Execute(context.Background(), BudgetDailyCheckInput{
		WorkspaceID: uuid.New(),
		MaxDailyUSD: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Exceeded || out.Warning80 {
		t.Errorf("expected within budget, got %+v", out)
	}
}

func TestBudgetDailyCheckActivityRequiresWorkspace(t *testing.T) {
	a := NewBudgetDailyCheckActivity(&fakeDaySpendReader{})

	if _, err := a.Execute(context.Background(), BudgetDailyCheckInput{MaxDailyUSD: 10}); err == nil {
		t.Fatal("expected workspace_id error")
	}
}

func TestBudgetDailyCheckActivityPropagatesReaderError(t *testing.T) {
	sentinel := errors.New("db unavailable")
	a := NewBudgetDailyCheckActivity(&fakeDaySpendReader{err: sentinel})

	_, err := a.Execute(context.Background(), BudgetDailyCheckInput{
		WorkspaceID: uuid.New(),
		MaxDailyUSD: 10,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
}

func TestNewPostgresDaySpendReaderNilPool(t *testing.T) {
	if reader := NewPostgresDaySpendReader(nil); reader != nil {
		t.Fatalf("expected nil reader for nil pool, got %T", reader)
	}
}
