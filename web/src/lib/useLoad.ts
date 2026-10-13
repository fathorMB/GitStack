import { useCallback, useEffect, useState } from 'react';
import { describeError } from './http';

// Carica dati all'ingresso della pagina: { data, loading, error, reload }.
// Se initial è fornito, lo usa come dato iniziale e salta il fetch.
export function useLoad<T>(fn: () => Promise<T>, initial?: T | null) {
  const [data, setData] = useState<T | null>(initial ?? null);
  const [loading, setLoading] = useState(initial === undefined);
  const [error, setError] = useState<string | null>(null);

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableFn = useCallback(fn, []);

  const reload = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setData(await stableFn());
    } catch (err) {
      setError(describeError(err));
    } finally {
      setLoading(false);
    }
  }, [stableFn]);

  // Se initial è fornito, usa quello e salta il fetch.
  useEffect(() => {
    if (initial !== undefined) return;
    void reload();
  }, [reload, initial]);

  return { data, setData, loading, error, reload };
}
