import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useLoad } from '../lib/useLoad';

describe('useLoad', () => {
  it('loading true al mount senza initial', async () => {
    const fn = vi.fn().mockResolvedValue('data');
    const { result } = renderHook(() => useLoad(fn));
    // Al mount, senza initial, loading deve essere true.
    expect(result.current.loading).toBe(true);
    expect(result.current.data).toBe(null);
    // Dopo il resolve, loading diventa false.
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.data).toBe('data');
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('loading false al mount con initial (dati già pronti)', async () => {
    const fn = vi.fn();
    const { result } = renderHook(() => useLoad(fn, 'initial'));
    expect(result.current.loading).toBe(false);
    expect(result.current.data).toBe('initial');
    // La funzione non viene richiamata.
    expect(fn).not.toHaveBeenCalled();
  });
});
