"use client";

// tar.ts — minimal USTAR encoder/decoder for the browser sandbox.
//
// Go's archive/tar (used by phone_backend.go export) writes plain USTAR headers
// for short paths, which is all the bundle uses (`<slug>/data/app.sqlite` etc).
// We produce the same so the Go importer (decodeBundleParts) reads our bundles,
// and we read Go-produced tars on import. PAX/GNU extended headers (long names)
// are skipped on read; our writer splits long names into the ustar prefix field.

export interface TarEntry {
  name: string;
  data: Uint8Array;
  mode?: number; // octal file mode, default 0o644
}

const BLOCK = 512;
const enc = new TextEncoder();
const dec = new TextDecoder();

function writeString(buf: Uint8Array, offset: number, str: string, max: number): void {
  const bytes = enc.encode(str);
  const n = Math.min(bytes.length, max);
  buf.set(bytes.subarray(0, n), offset);
}

// USTAR numeric fields: (len-1) octal digits, zero-padded, then NUL.
function writeOctal(buf: Uint8Array, offset: number, value: number, len: number): void {
  const digits = len - 1;
  const s = value.toString(8).padStart(digits, "0").slice(-digits);
  writeString(buf, offset, s, digits);
  buf[offset + digits] = 0;
}

function splitName(name: string): { name: string; prefix: string } {
  if (enc.encode(name).length <= 100) return { name, prefix: "" };
  // Split on a '/' so name <= 100 and prefix <= 155.
  let cut = name.lastIndexOf("/", name.length - 1);
  while (cut > 0) {
    const tail = name.slice(cut + 1);
    const head = name.slice(0, cut);
    if (enc.encode(tail).length <= 100 && enc.encode(head).length <= 155) {
      return { name: tail, prefix: head };
    }
    cut = name.lastIndexOf("/", cut - 1);
  }
  // Fall back to truncation (should never happen for sandbox paths).
  return { name: name.slice(-100), prefix: "" };
}

function header(entry: TarEntry): Uint8Array {
  const h = new Uint8Array(BLOCK);
  const { name, prefix } = splitName(entry.name);
  writeString(h, 0, name, 100);
  writeOctal(h, 100, entry.mode ?? 0o644, 8);
  writeOctal(h, 108, 0, 8); // uid
  writeOctal(h, 116, 0, 8); // gid
  writeOctal(h, 124, entry.data.length, 12); // size
  writeOctal(h, 136, 0, 12); // mtime (0 = deterministic bundles)
  h[156] = 0x30; // typeflag '0' (regular file)
  writeString(h, 257, "ustar", 6);
  h[263] = 0x30; // version "00"
  h[264] = 0x30;
  if (prefix) writeString(h, 345, prefix, 155);

  // Checksum: fill field with spaces, sum all bytes, write octal.
  for (let i = 148; i < 156; i++) h[i] = 0x20;
  let sum = 0;
  for (let i = 0; i < BLOCK; i++) sum += h[i];
  const chk = sum.toString(8).padStart(6, "0").slice(-6);
  writeString(h, 148, chk, 6);
  h[154] = 0; // NUL
  h[155] = 0x20; // space
  return h;
}

export function createTar(entries: TarEntry[]): Uint8Array {
  const chunks: Uint8Array[] = [];
  let total = 0;
  const push = (b: Uint8Array) => {
    chunks.push(b);
    total += b.length;
  };
  for (const e of entries) {
    push(header(e));
    push(e.data);
    const pad = (BLOCK - (e.data.length % BLOCK)) % BLOCK;
    if (pad) push(new Uint8Array(pad));
  }
  push(new Uint8Array(BLOCK * 2)); // end-of-archive marker
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}

function readOctal(buf: Uint8Array, offset: number, len: number): number {
  let s = dec.decode(buf.subarray(offset, offset + len)).replace(/[\0 ]+$/g, "").trim();
  if (!s) return 0;
  return parseInt(s, 8) || 0;
}

function readString(buf: Uint8Array, offset: number, len: number): string {
  const slice = buf.subarray(offset, offset + len);
  let end = slice.indexOf(0);
  if (end < 0) end = slice.length;
  return dec.decode(slice.subarray(0, end));
}

export function extractTar(data: Uint8Array): TarEntry[] {
  const out: TarEntry[] = [];
  let off = 0;
  while (off + BLOCK <= data.length) {
    const h = data.subarray(off, off + BLOCK);
    // Two consecutive zero blocks (or a zero block) => end of archive.
    if (h.every((b) => b === 0)) break;
    off += BLOCK;

    const namePart = readString(h, 0, 100);
    const prefix = readString(h, 345, 155);
    const fullName = prefix ? `${prefix}/${namePart}` : namePart;
    const size = readOctal(h, 124, 12);
    const typeflag = h[156];

    const fileData = data.subarray(off, off + size);
    off += size + ((BLOCK - (size % BLOCK)) % BLOCK);

    // '0' or '\0' = regular file. Skip directories ('5'), PAX ('x','g'), links.
    if (typeflag === 0x30 || typeflag === 0) {
      out.push({ name: fullName, data: new Uint8Array(fileData) });
    }
  }
  return out;
}
