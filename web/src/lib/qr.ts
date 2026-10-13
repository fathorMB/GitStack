// Encoder QR Code (ISO/IEC 18004), solo quello che serve alla UI: modo byte,
// livello di correzione M, versioni 1-10 (fino a 213 byte, piu' che
// sufficienti per un URI otpauth://). Nessuna dipendenza: serve a mostrare il
// segreto TOTP come QR senza aggiungere un pacchetto a web/ (lockfile e
// superficie di supply chain). Algoritmo e costanti sono quelli della norma.
//
// Uso: qrMatrix('otpauth://...') -> boolean[][] (true = modulo scuro), poi
// qrSvgPath(matrix) per disegnarlo.

// Livello M: [codeword ECC per blocco, blocchi gruppo 1, dati per blocco g1,
// blocchi gruppo 2, dati per blocco g2], versioni 1..10.
const BLOCKS_M: ReadonlyArray<readonly [number, number, number, number, number]> = [
  [10, 1, 16, 0, 0],
  [16, 1, 28, 0, 0],
  [26, 1, 44, 0, 0],
  [18, 2, 32, 0, 0],
  [24, 2, 43, 0, 0],
  [16, 4, 27, 0, 0],
  [18, 4, 31, 0, 0],
  [22, 2, 38, 2, 39],
  [22, 3, 36, 2, 37],
  [26, 4, 43, 1, 44],
];

const MAX_VERSION = BLOCKS_M.length;

function dataCodewords(version: number): number {
  const [, b1, d1, b2, d2] = BLOCKS_M[version - 1];
  return b1 * d1 + b2 * d2;
}

/** Quanti byte entrano nella versione (modo byte, livello M). */
export function qrCapacity(version: number): number {
  const countBits = version >= 10 ? 16 : 8;
  return Math.floor((dataCodewords(version) * 8 - 4 - countBits) / 8);
}

export function qrMaxBytes(): number {
  return qrCapacity(MAX_VERSION);
}

// ---- Reed-Solomon su GF(256), polinomio 0x11D ----

function gfMul(x: number, y: number): number {
  let z = 0;
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d);
    z ^= ((y >>> i) & 1) * x;
  }
  return z;
}

function rsDivisor(degree: number): number[] {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      result[j] = gfMul(result[j], root);
      if (j + 1 < result.length) result[j] ^= result[j + 1];
    }
    root = gfMul(root, 0x02);
  }
  return result;
}

function rsRemainder(data: readonly number[], divisor: readonly number[]): number[] {
  const result = divisor.map(() => 0);
  for (const b of data) {
    const factor = b ^ (result.shift() as number);
    result.push(0);
    divisor.forEach((coef, i) => {
      result[i] ^= gfMul(coef, factor);
    });
  }
  return result;
}

// ---- codeword dei dati ----

function utf8(text: string): number[] {
  return Array.from(new TextEncoder().encode(text));
}

function buildDataCodewords(bytes: readonly number[], version: number): number[] {
  const bits: number[] = [];
  const push = (value: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };
  push(0b0100, 4); // modo byte
  push(bytes.length, version >= 10 ? 16 : 8);
  for (const b of bytes) push(b, 8);
  const capacityBits = dataCodewords(version) * 8;
  push(0, Math.min(4, capacityBits - bits.length)); // terminatore
  while (bits.length % 8 !== 0) bits.push(0);
  const out: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let v = 0;
    for (let j = 0; j < 8; j++) v = (v << 1) | bits[i + j];
    out.push(v);
  }
  for (let pad = 0xec; out.length < dataCodewords(version); pad ^= 0xec ^ 0x11) out.push(pad);
  return out;
}

// Divide in blocchi, calcola l'ECC e interleava dati ed ECC.
function addEccAndInterleave(data: readonly number[], version: number): number[] {
  const [eccLen, b1, d1, b2, d2] = BLOCKS_M[version - 1];
  const divisor = rsDivisor(eccLen);
  const blocks: number[][] = [];
  const eccs: number[][] = [];
  let k = 0;
  const sizes = [...new Array<number>(b1).fill(d1), ...new Array<number>(b2).fill(d2)];
  for (const size of sizes) {
    const dat = data.slice(k, k + size);
    k += size;
    blocks.push(dat);
    eccs.push(rsRemainder(dat, divisor));
  }
  const out: number[] = [];
  const longest = Math.max(d1, d2);
  for (let i = 0; i < longest; i++) {
    for (const b of blocks) if (i < b.length) out.push(b[i]);
  }
  for (let i = 0; i < eccLen; i++) {
    for (const e of eccs) out.push(e[i]);
  }
  return out;
}

// ---- matrice ----

class Matrix {
  readonly size: number;
  readonly modules: boolean[][];
  readonly isFunction: boolean[][];

  constructor(readonly version: number) {
    this.size = version * 4 + 17;
    this.modules = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false));
    this.isFunction = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false));
    this.drawFunctionPatterns();
  }

  private setFunction(x: number, y: number, dark: boolean) {
    this.modules[y][x] = dark;
    this.isFunction[y][x] = true;
  }

  private drawFunctionPatterns() {
    const n = this.size;
    for (let i = 0; i < n; i++) {
      this.setFunction(6, i, i % 2 === 0);
      this.setFunction(i, 6, i % 2 === 0);
    }
    this.drawFinder(3, 3);
    this.drawFinder(n - 4, 3);
    this.drawFinder(3, n - 4);
    const pos = this.alignmentPositions();
    const last = pos.length - 1;
    for (let i = 0; i < pos.length; i++) {
      for (let j = 0; j < pos.length; j++) {
        if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) continue;
        this.drawAlignment(pos[i], pos[j]);
      }
    }
    this.drawFormat(0);
    this.drawVersion();
  }

  private drawFinder(cx: number, cy: number) {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy));
        const x = cx + dx;
        const y = cy + dy;
        if (x >= 0 && x < this.size && y >= 0 && y < this.size) this.setFunction(x, y, dist !== 2 && dist !== 4);
      }
    }
  }

  private drawAlignment(cx: number, cy: number) {
    for (let dy = -2; dy <= 2; dy++) {
      for (let dx = -2; dx <= 2; dx++) this.setFunction(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
    }
  }

  private alignmentPositions(): number[] {
    if (this.version === 1) return [];
    const count = Math.floor(this.version / 7) + 2;
    const step = Math.ceil((this.version * 4 + 4) / (count * 2 - 2)) * 2;
    const result = [6];
    for (let pos = this.size - 7; result.length < count; pos -= step) result.splice(1, 0, pos);
    return result;
  }

  /** Informazioni di formato: livello M (00) e maschera. */
  drawFormat(mask: number) {
    const data = (0b00 << 3) | mask;
    let rem = data;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const bits = ((data << 10) | rem) ^ 0x5412;
    const bit = (i: number) => ((bits >>> i) & 1) !== 0;
    const n = this.size;
    for (let i = 0; i <= 5; i++) this.setFunction(8, i, bit(i));
    this.setFunction(8, 7, bit(6));
    this.setFunction(8, 8, bit(7));
    this.setFunction(7, 8, bit(8));
    for (let i = 9; i < 15; i++) this.setFunction(14 - i, 8, bit(i));
    for (let i = 0; i < 8; i++) this.setFunction(n - 1 - i, 8, bit(i));
    for (let i = 8; i < 15; i++) this.setFunction(8, n - 15 + i, bit(i));
    this.setFunction(8, n - 8, true); // modulo scuro fisso
  }

  private drawVersion() {
    if (this.version < 7) return;
    let rem = this.version;
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    const bits = (this.version << 12) | rem;
    for (let i = 0; i < 18; i++) {
      const dark = ((bits >>> i) & 1) !== 0;
      const a = this.size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      this.setFunction(a, b, dark);
      this.setFunction(b, a, dark);
    }
  }

  drawCodewords(data: readonly number[]) {
    let i = 0;
    for (let right = this.size - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (let vert = 0; vert < this.size; vert++) {
        for (let j = 0; j < 2; j++) {
          const x = right - j;
          const upward = ((right + 1) & 2) === 0;
          const y = upward ? this.size - 1 - vert : vert;
          if (!this.isFunction[y][x] && i < data.length * 8) {
            this.modules[y][x] = ((data[i >>> 3] >>> (7 - (i & 7))) & 1) !== 0;
            i++;
          }
        }
      }
    }
  }

  applyMask(mask: number) {
    for (let y = 0; y < this.size; y++) {
      for (let x = 0; x < this.size; x++) {
        let invert: boolean;
        switch (mask) {
          case 0:
            invert = (x + y) % 2 === 0;
            break;
          case 1:
            invert = y % 2 === 0;
            break;
          case 2:
            invert = x % 3 === 0;
            break;
          case 3:
            invert = (x + y) % 3 === 0;
            break;
          case 4:
            invert = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
            break;
          case 5:
            invert = ((x * y) % 2) + ((x * y) % 3) === 0;
            break;
          case 6:
            invert = (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
            break;
          default:
            invert = (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
        }
        if (!this.isFunction[y][x] && invert) this.modules[y][x] = !this.modules[y][x];
      }
    }
  }

  penalty(): number {
    const n = this.size;
    let score = 0;
    const line = (get: (i: number) => boolean) => {
      let run = 1;
      let s = '';
      for (let i = 0; i < n; i++) {
        s += get(i) ? '1' : '0';
        if (i > 0 && get(i) === get(i - 1)) {
          run++;
          if (run === 5) score += 3;
          else if (run > 5) score += 1;
        } else {
          run = 1;
        }
      }
      // Pattern simile a un finder (1:1:3:1:1) con 4 moduli chiari da un lato.
      for (const p of ['10111010000', '00001011101']) {
        for (let at = s.indexOf(p); at !== -1; at = s.indexOf(p, at + 1)) score += 40;
      }
    };
    for (let y = 0; y < n; y++) line((x) => this.modules[y][x]);
    for (let x = 0; x < n; x++) line((y) => this.modules[y][x]);
    let dark = 0;
    for (let y = 0; y < n; y++) {
      for (let x = 0; x < n; x++) {
        const c = this.modules[y][x];
        if (c) dark++;
        if (x + 1 < n && y + 1 < n && c === this.modules[y][x + 1] && c === this.modules[y + 1][x] && c === this.modules[y + 1][x + 1]) {
          score += 3;
        }
      }
    }
    const total = n * n;
    score += (Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1) * 10;
    return score;
  }
}

/**
 * Codifica `text` (UTF-8) come QR (modo byte, livello M, versione minima).
 * Lancia un errore se non entra nella versione 10.
 */
export function qrMatrix(text: string): boolean[][] {
  const bytes = utf8(text);
  let version = 1;
  while (version <= MAX_VERSION && bytes.length > qrCapacity(version)) version++;
  if (version > MAX_VERSION) {
    throw new Error(`Testo troppo lungo per il QR (${bytes.length} byte, massimo ${qrMaxBytes()}).`);
  }
  const codewords = addEccAndInterleave(buildDataCodewords(bytes, version), version);
  let best: Matrix | null = null;
  let bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    const m = new Matrix(version);
    m.drawCodewords(codewords);
    m.applyMask(mask);
    m.drawFormat(mask);
    const p = m.penalty();
    if (p < bestScore) {
      best = m;
      bestScore = p;
    }
  }
  return (best as Matrix).modules;
}

/** Percorso SVG (un quadrato 1x1 per modulo scuro) per la matrice, senza quiet zone. */
export function qrSvgPath(matrix: readonly (readonly boolean[])[]): string {
  const parts: string[] = [];
  matrix.forEach((row, y) => {
    row.forEach((dark, x) => {
      if (dark) parts.push(`M${x} ${y}h1v1h-1z`);
    });
  });
  return parts.join('');
}
