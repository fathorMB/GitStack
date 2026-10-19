package ci

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MinScheduleInterval è il tempo minimo fra due scatti di uno schedule (CI3).
const MinScheduleInterval = 15 * time.Minute

// Cron è un'espressione cron a 5 campi (minuto, ora, giorno del mese, mese,
// giorno della settimana), sempre in UTC come GitHub.
type Cron struct {
	min, hour, dom, mon, dow uint64
	domStar, dowStar         bool
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// ParseCron legge l'espressione. Non controlla la frequenza: vedi ValidateCron.
func ParseCron(expr string) (*Cron, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, fmt.Errorf("servono 5 campi, ce ne sono %d", len(f))
	}
	var c Cron
	var err error
	if c.min, _, err = parseCronField(f[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minuto: %w", err)
	}
	if c.hour, _, err = parseCronField(f[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("ora: %w", err)
	}
	if c.dom, c.domStar, err = parseCronField(f[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("giorno del mese: %w", err)
	}
	if c.mon, _, err = parseCronField(f[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("mese: %w", err)
	}
	if c.dow, c.dowStar, err = parseCronField(f[4], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("giorno della settimana: %w", err)
	}
	if c.dow&(1<<7) != 0 { // 7 = domenica
		c.dow |= 1
	}
	return &c, nil
}

// ValidateCron controlla che l'espressione sia valida e che due scatti
// consecutivi distino almeno 15 minuti (CI3).
func ValidateCron(expr string) error {
	c, err := ParseCron(expr)
	if err != nil {
		return err
	}
	if gap := c.MinGap(); gap < MinScheduleInterval {
		return fmt.Errorf("scatta ogni %d minuti: il minimo è %d", int(gap/time.Minute), int(MinScheduleInterval/time.Minute))
	}
	return nil
}

func parseCronField(s string, lo, hi int, names map[string]int) (set uint64, star bool, err error) {
	star = s == "*" || strings.HasPrefix(s, "*/")
	for _, part := range strings.Split(s, ",") {
		step := 1
		if base, st, ok := strings.Cut(part, "/"); ok {
			n, e := strconv.Atoi(st)
			if e != nil || n < 1 {
				return 0, false, fmt.Errorf("passo %q non valido", st)
			}
			step, part = n, base
		}
		a, b := lo, hi
		if part != "*" {
			from, to, isRange := strings.Cut(part, "-")
			x, e := cronNum(from, names)
			if e != nil {
				return 0, false, e
			}
			a, b = x, x
			if isRange {
				if b, e = cronNum(to, names); e != nil {
					return 0, false, e
				}
			} else if step > 1 {
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return 0, false, fmt.Errorf("%q fuori dall'intervallo %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, star, nil
}

func cronNum(s string, names map[string]int) (int, error) {
	if v, ok := names[strings.ToLower(s)]; ok {
		return v, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q non è un numero", s)
	}
	return n, nil
}

// Matches dice se l'istante t (al minuto, UTC) è uno scatto.
func (c *Cron) Matches(t time.Time) bool {
	t = t.UTC()
	if c.min&(1<<uint(t.Minute())) == 0 || c.hour&(1<<uint(t.Hour())) == 0 || c.mon&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	// Come cron: se entrambi i campi sono ristretti basta uno dei due.
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowOK
	case c.dowStar:
		return domOK
	}
	return domOK || dowOK
}

// Next è il primo scatto strettamente dopo after; zero se non ce n'è entro 5 anni.
func (c *Cron) Next(after time.Time) time.Time {
	t := after.UTC().Truncate(time.Minute).Add(time.Minute)
	end := t.AddDate(5, 0, 0)
	for t.Before(end) {
		// Salti grossolani: mese e giorno non corrispondono, si passa al prossimo giorno.
		if c.mon&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !c.dayOK(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if c.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (c *Cron) dayOK(t time.Time) bool {
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowOK
	case c.dowStar:
		return domOK
	}
	return domOK || dowOK
}

// MinGap è la distanza minima fra due scatti consecutivi, su un anno (un anno
// bisestile intero copre mese e giorno). Se lo schedule scatta una sola volta
// all'anno o mai, vale un anno.
func (c *Cron) MinGap() time.Duration {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // bisestile
	end := start.AddDate(1, 0, 0)
	best := 366 * 24 * time.Hour
	prev := time.Time{}
	for t := c.Next(start.Add(-time.Minute)); !t.IsZero() && t.Before(end); t = c.Next(t) {
		if !prev.IsZero() {
			if g := t.Sub(prev); g < best {
				best = g
				if best < MinScheduleInterval {
					return best
				}
			}
		}
		prev = t
	}
	return best
}
