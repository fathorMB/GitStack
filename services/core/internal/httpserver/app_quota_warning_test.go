package httpserver

import (
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// quotaToAPI è l'unico punto che calcola l'avviso: lo usano ListApps, il
// dettaglio dei volumi e l'elenco admin (GIT-405).
func TestQuotaToAPI_AvvisoAlBordoDell80(t *testing.T) {
	casi := []struct {
		nome         string
		used, limits store.AppQuotaAmounts
		want         bool
	}{
		{"79,9% cpu", store.AppQuotaAmounts{CPUMillis: 799}, store.AppQuotaAmounts{CPUMillis: 1000}, false},
		{"80% cpu", store.AppQuotaAmounts{CPUMillis: 800}, store.AppQuotaAmounts{CPUMillis: 1000}, true},
		{"79,9% memoria", store.AppQuotaAmounts{MemoryBytes: 799}, store.AppQuotaAmounts{MemoryBytes: 1000}, false},
		{"80% memoria", store.AppQuotaAmounts{MemoryBytes: 800}, store.AppQuotaAmounts{MemoryBytes: 1000}, true},
		{"79,9% disco", store.AppQuotaAmounts{DiskBytes: 799}, store.AppQuotaAmounts{DiskBytes: 1000}, false},
		{"80% disco", store.AppQuotaAmounts{DiskBytes: 800}, store.AppQuotaAmounts{DiskBytes: 1000}, true},
		{"limite 0 e uso > 0", store.AppQuotaAmounts{CPUMillis: 5, MemoryBytes: 5, DiskBytes: 5}, store.AppQuotaAmounts{}, false},
	}
	for _, c := range casi {
		t.Run(c.nome, func(t *testing.T) {
			q := quotaToAPI(store.AppOwnerQuota{Owner: "o", Limits: c.limits, Used: c.used})
			if q.Warning != c.want {
				t.Errorf("Warning = %v, atteso %v", q.Warning, c.want)
			}
		})
	}
}
