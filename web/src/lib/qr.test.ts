import { describe, expect, it } from 'vitest';
import { qrCapacity, qrMatrix, qrMaxBytes, qrSvgPath } from './qr';

// Informazioni di formato per il livello M e le maschere 0-7 (tabella di ISO/IEC 18004).
const FORMAT_M = [
  '101010000010010',
  '101000100100101',
  '101111001111100',
  '101101101001011',
  '100010111111001',
  '100000011001110',
  '100111110010111',
  '100101010100000',
];

function formatBits(m: boolean[][]): string {
  const n = m.length;
  // Prima copia, intorno al finder in alto a sinistra: come da norma, il bit 14 e' il primo.
  const at = (x: number, y: number) => (m[y][x] ? '1' : '0');
  const cells: Array<[number, number]> = [
    [8, 0], [8, 1], [8, 2], [8, 3], [8, 4], [8, 5], [8, 7], [8, 8], [7, 8],
    [5, 8], [4, 8], [3, 8], [2, 8], [1, 8], [0, 8],
  ];
  // cells[i] e' il bit i (0 = meno significativo): la stringa va dal bit 14 al bit 0.
  const first = cells.map(([x, y]) => at(x, y)).reverse().join('');
  // Seconda copia: deve coincidere con la prima.
  const second: string[] = [];
  for (let i = 0; i < 8; i++) second.push(at(n - 1 - i, 8));
  for (let i = 8; i < 15; i++) second.push(at(8, n - 15 + i));
  expect(second.reverse().join('')).toBe(first);
  return first;
}

describe('qr', () => {
  it('sceglie la versione minima e disegna finder, timing e modulo scuro', () => {
    const small = qrMatrix('A');
    expect(small).toHaveLength(21);
    const m = qrMatrix('x'.repeat(qrCapacity(1) + 1));
    expect(m).toHaveLength(25); // versione 2
    for (const mat of [small, m]) {
      const n = mat.length;
      // finder: 7x7 con anello scuro, anello chiaro e centro 3x3 scuro.
      for (const [ox, oy] of [[0, 0], [n - 7, 0], [0, n - 7]]) {
        expect(mat[oy][ox]).toBe(true);
        expect(mat[oy + 1][ox + 1]).toBe(false);
        expect(mat[oy + 3][ox + 3]).toBe(true);
        expect(mat[oy + 6][ox + 6]).toBe(true);
      }
      // timing alternato fra i finder.
      for (let i = 8; i < n - 8; i++) {
        expect(mat[6][i]).toBe(i % 2 === 0);
        expect(mat[i][6]).toBe(i % 2 === 0);
      }
      expect(mat[n - 8][8]).toBe(true);
    }
  });

  it('scrive informazioni di formato valide per il livello M, identiche nelle due copie', () => {
    for (const text of ['A', 'otpauth://totp/GitStack:alice?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=GitStack']) {
      expect(FORMAT_M).toContain(formatBits(qrMatrix(text)));
    }
  });

  it('scrive le informazioni di versione dalla 7 in poi', () => {
    const m = qrMatrix('x'.repeat(qrCapacity(7)));
    expect(m).toHaveLength(17 + 4 * 7);
    // Versione 7 = 000111 + BCH 110010010100: i 18 bit, letti a terne.
    const n = m.length;
    let bits = 0;
    for (let i = 0; i < 18; i++) {
      if (m[Math.floor(i / 3)][n - 11 + (i % 3)]) bits |= 1 << i;
    }
    expect(bits).toBe(0x07c94);
  });

  it('copre tutte le versioni fino a 10 e rifiuta oltre', () => {
    for (let v = 1; v <= 10; v++) {
      expect(qrMatrix('y'.repeat(qrCapacity(v)))).toHaveLength(17 + 4 * v);
    }
    expect(qrMaxBytes()).toBe(qrCapacity(10));
    expect(() => qrMatrix('z'.repeat(qrMaxBytes() + 1))).toThrow(/troppo lungo/);
  });

  it('e deterministico e codifica UTF-8', () => {
    expect(qrMatrix('caffè ☕')).toEqual(qrMatrix('caffè ☕'));
    expect(qrMatrix('caffè')).not.toEqual(qrMatrix('caffe'));
  });

  it('il percorso SVG ha un quadrato per modulo scuro', () => {
    const m = qrMatrix('hello');
    const dark = m.flat().filter(Boolean).length;
    expect(qrSvgPath(m).split('z').length - 1).toBe(dark);
  });
});
