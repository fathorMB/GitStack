import { useCallback, useEffect, useState } from 'react';
import { describeError } from './http';

// Carica dati all'ingresso della pagina: { data, loading, error, reload }.
export function useLoad<T>(fn: () => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
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

  useEffect(() => {
    void reload();
  }, [reload]);

  return { data, setData, loading, error, reload };
}
