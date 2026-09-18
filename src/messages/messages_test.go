package messages

import (
	"encoding/json"
	"testing"
)

// TestNew covers the happy path of loading the embedded message list and
// verifies the manager starts in a clean, well-formed state.
func TestNew(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if m == nil {
		t.Fatal("New() returned nil manager")
	}
	if m.Count() == 0 {
		t.Fatal("expected at least one embedded message, got 0")
	}
	if m.cycle != 1 {
		t.Fatalf("expected initial cycle 1, got %d", m.cycle)
	}
}

// TestGetRandom_ReturnsKnownMessage checks the happy path: a returned
// message must come from the loaded set.
func TestGetRandom_ReturnsKnownMessage(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	known := make(map[string]bool, len(m.messages))
	for _, msg := range m.messages {
		known[msg] = true
	}

	msg, err := m.GetRandom()
	if err != nil {
		t.Fatalf("GetRandom() error: %v", err)
	}
	if !known[msg] {
		t.Fatalf("GetRandom() returned message not in the set: %q", msg)
	}
}

// TestGetRandom_EmptyMessages is a boundary test: an empty message list
// must return an error, not panic or loop forever.
func TestGetRandom_EmptyMessages(t *testing.T) {
	m := &Manager{
		messages:    []string{},
		usedIndexes: make(map[int]bool),
		cycle:       1,
	}
	if _, err := m.GetRandom(); err == nil {
		t.Fatal("expected error for empty messages, got nil")
	}
}

// TestGetRandom_NoDuplicatesUntilExhausted is the core behavioral
// guarantee of the manager: within a cycle no message repeats until every
// message has been used, at which point a new cycle starts.
func TestGetRandom_NoDuplicatesUntilExhausted(t *testing.T) {
	m := &Manager{
		messages:    []string{"a", "b", "c"},
		usedIndexes: make(map[int]bool),
		cycle:       1,
	}

	seen := make(map[string]int)
	for i := 0; i < 3; i++ {
		msg, err := m.GetRandom()
		if err != nil {
			t.Fatalf("GetRandom() error on iteration %d: %v", i, err)
		}
		seen[msg]++
	}

	for _, msg := range m.messages {
		if seen[msg] != 1 {
			t.Errorf("message %q seen %d times in one cycle, want exactly 1", msg, seen[msg])
		}
	}
	if m.cycle != 1 {
		t.Fatalf("cycle should still be 1 after exactly exhausting the set, got %d", m.cycle)
	}

	// One more pull should roll into a new cycle and succeed.
	if _, err := m.GetRandom(); err != nil {
		t.Fatalf("GetRandom() after exhaustion error: %v", err)
	}
	if m.cycle != 2 {
		t.Fatalf("expected cycle to advance to 2, got %d", m.cycle)
	}
	if len(m.usedIndexes) != 1 {
		t.Fatalf("expected exactly 1 used index in new cycle, got %d", len(m.usedIndexes))
	}
}

// TestGetRandom_SingleMessage is a boundary test with exactly one message.
func TestGetRandom_SingleMessage(t *testing.T) {
	m := &Manager{
		messages:    []string{"only"},
		usedIndexes: make(map[int]bool),
		cycle:       1,
	}
	msg, err := m.GetRandom()
	if err != nil {
		t.Fatalf("GetRandom() error: %v", err)
	}
	if msg != "only" {
		t.Fatalf("expected %q, got %q", "only", msg)
	}
	// Next call must roll into a new cycle since the only message is used.
	msg2, err := m.GetRandom()
	if err != nil {
		t.Fatalf("GetRandom() second call error: %v", err)
	}
	if msg2 != "only" {
		t.Fatalf("expected %q, got %q", "only", msg2)
	}
	if m.cycle != 2 {
		t.Fatalf("expected cycle 2, got %d", m.cycle)
	}
}

// TestGetAll verifies GetAll returns the full underlying slice.
func TestGetAll(t *testing.T) {
	m := &Manager{
		messages:    []string{"x", "y"},
		usedIndexes: make(map[int]bool),
		cycle:       1,
	}
	all := m.GetAll()
	if len(all) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(all))
	}
}

// TestGetAllJSON verifies the raw embedded JSON parses to a non-empty
// string slice, matching what loadMessages produces.
func TestGetAllJSON(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	data, err := m.GetAllJSON()
	if err != nil {
		t.Fatalf("GetAllJSON() error: %v", err)
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("GetAllJSON() did not return valid JSON array: %v", err)
	}
	if len(out) != m.Count() {
		t.Fatalf("GetAllJSON() length %d != Count() %d", len(out), m.Count())
	}
}

// TestCount checks the reported count matches the loaded messages.
func TestCount(t *testing.T) {
	m := &Manager{
		messages:    []string{"a", "b", "c", "d"},
		usedIndexes: make(map[int]bool),
		cycle:       1,
	}
	if m.Count() != 4 {
		t.Fatalf("expected Count() 4, got %d", m.Count())
	}
}

// TestStats verifies the stats map reflects cycle/usage bookkeeping.
func TestStats(t *testing.T) {
	m := &Manager{
		messages:    []string{"a", "b", "c"},
		usedIndexes: map[int]bool{0: true},
		cycle:       1,
	}
	stats := m.Stats()

	if stats["cycle"] != 1 {
		t.Errorf("cycle = %v, want 1", stats["cycle"])
	}
	if stats["total_messages"] != 3 {
		t.Errorf("total_messages = %v, want 3", stats["total_messages"])
	}
	if stats["used_in_cycle"] != 1 {
		t.Errorf("used_in_cycle = %v, want 1", stats["used_in_cycle"])
	}
	if stats["remaining_in_cycle"] != 2 {
		t.Errorf("remaining_in_cycle = %v, want 2", stats["remaining_in_cycle"])
	}
}

// TestResetCycle verifies ResetCycle clears usage and advances the cycle,
// which is the same behavior GetRandom relies on when exhausted.
func TestResetCycle(t *testing.T) {
	m := &Manager{
		messages:    []string{"a", "b"},
		usedIndexes: map[int]bool{0: true, 1: true},
		cycle:       3,
	}
	m.ResetCycle()
	if m.cycle != 4 {
		t.Fatalf("expected cycle 4 after reset, got %d", m.cycle)
	}
	if len(m.usedIndexes) != 0 {
		t.Fatalf("expected usedIndexes cleared, got %d entries", len(m.usedIndexes))
	}
}

// TestGetRandom_Concurrent exercises the manager under concurrent access to
// make sure the mutex actually protects shared state (no races, no panics).
func TestGetRandom_Concurrent(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func() {
			_, err := m.GetRandom()
			done <- err
		}()
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent GetRandom() error: %v", err)
		}
	}
}
