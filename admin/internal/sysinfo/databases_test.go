package sysinfo

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestDatabasesPreflight(t *testing.T) {
	cases := []struct {
		name         string
		p            NodeProbe
		fails, warns int
	}{
		{"consigliato", NodeProbe{MemoryBytes: 16 * gb, DiskFreeBytes: 120 * gb}, 0, 0},
		{"sotto il consigliato", NodeProbe{MemoryBytes: 12 * gb, DiskFreeBytes: 30 * gb}, 0, 2},
		{"profilo minimo di GitStack", NodeProbe{MemoryBytes: 8 * gb, DiskFreeBytes: 20 * gb}, 2, 0},
		{"solo RAM corta", NodeProbe{MemoryBytes: 8 * gb, DiskFreeBytes: 120 * gb}, 1, 0},
		{"misure non lette", NodeProbe{}, 0, 0},
	}
	for _, c := range cases {
		f, w := DatabasesPreflight(c.p)
		if len(f) != c.fails || len(w) != c.warns {
			t.Errorf("%s: fails=%v warns=%v", c.name, f, w)
		}
	}
}

// I valori devono essere gli stessi di deploy/install.sh (DB_MIN_*/DB_REC_*).
func TestDatabasesPreflight_ComeInstallSh(t *testing.T) {
	b, err := os.ReadFile("../../../deploy/install.sh")
	if err != nil {
		t.Skipf("install.sh non raggiungibile: %v", err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for name, want := range map[string]int64{
		"DB_MIN_RAM_GB":       MinDatabasesMemoryBytes / gb,
		"DB_REC_RAM_GB":       RecDatabasesMemoryBytes / gb,
		"DB_MIN_DISK_FREE_GB": MinDatabasesDiskFreeBytes / gb,
		"DB_REC_DISK_FREE_GB": RecDatabasesDiskFreeBytes / gb,
	} {
		m := regexp.MustCompile(`(?m)^` + name + `=(\d+)$`).FindStringSubmatch(src)
		if m == nil {
			t.Errorf("%s non dichiarata in install.sh", name)
			continue
		}
		if got, _ := strconv.ParseInt(m[1], 10, 64); got != want {
			t.Errorf("%s: install.sh %d, sysinfo %d", name, got, want)
		}
	}
}
