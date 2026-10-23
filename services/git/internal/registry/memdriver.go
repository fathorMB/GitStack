package registry

import (
	"context"
	"sync"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	"github.com/distribution/distribution/v3/registry/storage/driver/inmemory"
)

// Archiviazione in memoria condivisa, solo per i test. Il driver «inmemory» di distribution
// crea un'istanza nuova a ogni factory.Create: l'app di distribution e il registro (pulizia,
// quota, tag protetti, Purge) vedrebbero archivi diversi. Qui l'istanza è una sola, indicata
// dall'id nei parametri.

const sharedMemoryDriver = "gitstack-shared-memory"

var (
	sharedMu      sync.Mutex
	sharedDrivers = map[string]storagedriver.StorageDriver{}
)

type sharedMemoryFactory struct{}

func (sharedMemoryFactory) Create(_ context.Context, params map[string]any) (storagedriver.StorageDriver, error) {
	id, _ := params["id"].(string)
	sharedMu.Lock()
	defer sharedMu.Unlock()
	d, ok := sharedDrivers[id]
	if !ok {
		d = inmemory.New()
		sharedDrivers[id] = d
	}
	return d, nil
}

func init() { factory.Register(sharedMemoryDriver, sharedMemoryFactory{}) }
