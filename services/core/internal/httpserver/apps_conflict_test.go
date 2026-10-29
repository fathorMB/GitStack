package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func TestWriteAppConflictCapacityMessages(t *testing.T) {
	const ownerMessage = "La quota delle app dell'owner non basta per le parti garantite."
	const reserveMessage = "La riserva delle app del server non basta per le parti garantite."
	const bothMessage = "La quota delle app dell'owner e la riserva delle app del server non bastano per le parti garantite."
	const diskMessage = "La quota disco dell'owner non basta per questo volume: mancano 512 byte."

	tests := []struct {
		name    string
		missing map[string]int64
		message string
	}{
		{name: "owner quota", missing: map[string]int64{"cpuMillis": 100}, message: ownerMessage},
		{name: "server reserve", missing: map[string]int64{"reserveCpuMillis": 100, "reserveMemoryBytes": 200}, message: reserveMessage},
		{name: "owner quota and server reserve", missing: map[string]int64{"memoryBytes": 200, "reserveCpuMillis": 100}, message: bothMessage},
		{name: "disk", missing: map[string]int64{"diskBytes": 512}, message: diskMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			err := &store.CapacityError{
				Owner:   "alice",
				Missing: tt.missing,
				Limits:  store.AppQuotaAmounts{CPUMillis: 1000, MemoryBytes: 2000, DiskBytes: 3000},
				Used:    store.AppQuotaAmounts{CPUMillis: 10, MemoryBytes: 20, DiskBytes: 30},
			}
			if !(*apiServer)(nil).writeAppConflict(rec, err) {
				t.Fatal("writeAppConflict ha rifiutato CapacityError")
			}
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, atteso %d", rec.Code, http.StatusConflict)
			}
			var got openapi.Error
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decodifica risposta: %v", err)
			}
			if got.Error.Code != "insufficient_capacity" || got.Error.Message != tt.message {
				t.Fatalf("errore = (%q, %q), atteso (%q, %q)", got.Error.Code, got.Error.Message, "insufficient_capacity", tt.message)
			}
			if got.Error.Details == nil {
				t.Fatal("details assenti")
			}
			details := *got.Error.Details
			if details["owner"] != "alice" || details["missing"] == nil || details["limits"] == nil || details["used"] == nil {
				t.Fatalf("details alterati o incompleti: %#v", details)
			}
		})
	}
}
