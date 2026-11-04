import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { LogView } from './LogView';
import type { LogViewLine } from './LogView';

const mk = (n: number, text = 'line'): LogViewLine[] =>
  Array.from({ length: n }, (_, i) => ({ key: i, prefix: String(i + 1), text: `${text} ${i + 1}` }));

/** jsdom non fa layout: scrollTop/scrollHeight/clientHeight si simulano sul riquadro. */
function fakeLayout(el: HTMLElement, scrollHeight: number, clientHeight = 100) {
  let top = 0;
  Object.defineProperty(el, 'scrollHeight', { configurable: true, get: () => scrollHeight });
  Object.defineProperty(el, 'clientHeight', { configurable: true, get: () => clientHeight });
  Object.defineProperty(el, 'scrollTop', { configurable: true, get: () => top, set: (v: number) => { top = v; } });
}

describe('LogView', () => {
  it('righe lunghe: il numero è solo sulla prima riga (il CSS gestisce il resto)', () => {
    render(<LogView label="Log" live={false} lines={[{ key: 1, prefix: '1', text: 'x'.repeat(500) }]} />);
    const row = screen.getByRole('log').querySelector('div.l') as HTMLElement;
    expect(row.querySelectorAll('.n')).toHaveLength(1);
    expect(row.textContent).toBe(`1${'x'.repeat(500)}`);
  }, 10000);

  it('a job in corso scorre all’ultima riga a ogni arrivo di righe', () => {
    const { rerender } = render(<LogView label="Log" live lines={mk(3)} />);
    const el = screen.getByRole('log');
    fakeLayout(el, 1000);
    rerender(<LogView label="Log" live lines={mk(5)} />);
    expect(el.scrollTop).toBe(1000);
  }, 10000);

  it('se l’utente scorre in su non viene riportato in fondo; il pulsante fa ripartire il segui', () => {
    const { rerender } = render(<LogView label="Log" live lines={mk(3)} />);
    const el = screen.getByRole('log');
    fakeLayout(el, 1000);
    el.scrollTop = 100;
    fireEvent.scroll(el);
    rerender(<LogView label="Log" live lines={mk(6)} />);
    expect(el.scrollTop).toBe(100);
    fireEvent.click(screen.getByRole('button', { name: /jump to bottom/i }));
    expect(el.scrollTop).toBe(1000);
    expect(screen.queryByRole('button', { name: /jump to bottom/i })).not.toBeInTheDocument();
    rerender(<LogView label="Log" live lines={mk(8)} />);
    expect(el.scrollTop).toBe(1000);
  }, 10000);

  it('tornando in fondo a mano il segui riparte', () => {
    const { rerender } = render(<LogView label="Log" live lines={mk(3)} />);
    const el = screen.getByRole('log');
    fakeLayout(el, 1000);
    el.scrollTop = 100;
    fireEvent.scroll(el);
    expect(screen.getByRole('button', { name: /jump to bottom/i })).toBeInTheDocument();
    el.scrollTop = 900;
    fireEvent.scroll(el);
    expect(screen.queryByRole('button', { name: /jump to bottom/i })).not.toBeInTheDocument();
    rerender(<LogView label="Log" live lines={mk(4)} />);
    expect(el.scrollTop).toBe(1000);
  }, 10000);

  it('a job concluso non scorre da solo e non mostra il pulsante', () => {
    const { rerender } = render(<LogView label="Log" live={false} lines={mk(3)} />);
    const el = screen.getByRole('log');
    fakeLayout(el, 1000);
    rerender(<LogView label="Log" live={false} lines={mk(6)} />);
    expect(el.scrollTop).toBe(0);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  }, 10000);
});
