package databases

import (
	"context"
	"errors"
	"fmt"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// RelinkAfterRestore rimette in piedi i collegamenti dopo un ripristino (ADR 0003, DB15, passo 4). I ruoli di
// login dei collegamenti non stanno nei dump (sono globali all'istanza): in un'istanza vuota non esistono, e in
// un ripristino sul posto conservano la vecchia password. In entrambi i casi ogni collegamento attivo riparte con
// una password NUOVA: se manca il ruolo lo si ricrea, poi si usa la rotazione di GIT-552 (RotateLink), che cambia la
// password, chiude le sessioni, riscrive i segreti dell'app e ne richiede il deploy (l'app riparte). Il vecchio
// segreto non entra piu'. E' l'AfterRestore predefinito quando c'e' il portachiavi dei segreti (Config.Secrets).
// Un collegamento in scollegamento si lascia a ResumeUnlinks.
func (c *Controller) RelinkAfterRestore(ctx context.Context, d store.ManagedDB) error {
	keys := c.cfg.Secrets
	if keys == nil || c.sql == nil {
		return nil
	}
	links, err := c.st.ListManagedDBLinks(ctx, d.ID)
	if err != nil {
		return err
	}
	// Il ripristino e' in corso e lo stato in memoria puo' essere `restoring`: i ruoli si toccano comunque, il
	// database e' pronto (e' per questo che il ripristino e' partito).
	d.Status = store.DBRunning
	var errs []error
	for _, l := range links {
		if l.State != "active" {
			continue
		}
		exists, err := RoleExists(ctx, c.sql, d, LinkRole(l.ID))
		if err != nil {
			errs = append(errs, fmt.Errorf("collegamento %s: %w", l.ID, err))
			continue
		}
		if !exists {
			tmp, err := NewPassword()
			if err != nil {
				return err
			}
			c.remember(tmp)
			if err := CreateLinkRole(ctx, c.sql, d, l.ID, l.Mode, tmp); err != nil {
				errs = append(errs, fmt.Errorf("collegamento %s: ruolo non ricreato: %w", l.ID, err))
				continue
			}
		}
		if _, err := c.RotateLink(ctx, keys, d, l, l.CreatedBy); err != nil {
			errs = append(errs, fmt.Errorf("collegamento %s: %w", l.ID, err))
		}
	}
	return errors.Join(errs...)
}

// afterRestore ritorna l'hook da chiamare dopo un ripristino riuscito: quello di Config se c'e', altrimenti la
// ricreazione dei collegamenti.
func (c *Controller) afterRestore() func(context.Context, store.ManagedDB) error {
	if c.cfg.AfterRestore != nil {
		return c.cfg.AfterRestore
	}
	return c.RelinkAfterRestore
}
