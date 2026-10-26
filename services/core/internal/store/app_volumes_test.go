package store

import "testing"

func TestValidateVolumeMountPath(t *testing.T) {
	for _, ok := range []string{"/data", "/var/lib/postgresql/data", "/srv/uploads", "/a.b/c-d_e"} {
		if msg := ValidateVolumeMountPath(ok); msg != "" {
			t.Errorf("%q deve andare bene: %s", ok, msg)
		}
	}
	for _, bad := range []string{"", "data", "/", "/data/", "/a/../b", "/a//b", "/./a", "/proc", "/proc/1", "/sys/x", "/dev", "/dev/shm", "/a:b", "/a,b", "/a\nb"} {
		if msg := ValidateVolumeMountPath(bad); msg == "" {
			t.Errorf("%q deve essere rifiutato", bad)
		}
	}
	long := "/" + string(make([]byte, 600))
	if ValidateVolumeMountPath(long) == "" {
		t.Error("oltre 512 caratteri deve essere rifiutato")
	}
	// /procfoo non è sotto /proc.
	if msg := ValidateVolumeMountPath("/procfoo"); msg != "" {
		t.Errorf("/procfoo non è /proc: %s", msg)
	}
}
