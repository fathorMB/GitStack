package databases

import "testing"

// GIT-557: le scelte della console (intervallo LAN, dump tenuti) hanno la precedenza sul chart e sul
// predefinito; zero torna al chart.
func TestRuntime_ScelteDellaConsole(t *testing.T) {
	c := &Controller{cfg: Config{LANPortFrom: 16000, LANPortTo: 16010, KeepScheduled: 4}}
	if f, to := c.EffectiveLANRange(); f != 16000 || to != 16010 {
		t.Errorf("intervallo del chart: %d-%d", f, to)
	}
	if s, o := c.EffectiveKeep(); s != 4 || o != 5 {
		t.Errorf("conservazione del chart/predefinita: %d %d", s, o)
	}
	c.SetRuntime(Runtime{LANPortFrom: 20000, LANPortTo: 20010, KeepScheduled: 2, KeepOnDemand: 9})
	if f, to := c.EffectiveLANRange(); f != 20000 || to != 20010 {
		t.Errorf("intervallo della console: %d-%d", f, to)
	}
	if s, o := c.EffectiveKeep(); s != 2 || o != 9 {
		t.Errorf("conservazione della console: %d %d", s, o)
	}
	if rt := c.RuntimeSettings(); rt.LANPortFrom != 20000 {
		t.Errorf("RuntimeSettings: %+v", rt)
	}
	c.SetRuntime(Runtime{})
	if f, to := c.EffectiveLANRange(); f != 16000 || to != 16010 {
		t.Errorf("zero torna al chart: %d-%d", f, to)
	}
	empty := &Controller{}
	if f, to := empty.EffectiveLANRange(); f != DefaultLANPortFrom || to != DefaultLANPortTo {
		t.Errorf("predefinito: %d-%d", f, to)
	}
}
