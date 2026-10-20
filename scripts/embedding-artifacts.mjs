import { createHash } from "node:crypto";

export function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

// Same sealed Mach-O normalization as backend/internal/embedding/assets.go.
// Signature offset stays hashed; only the final signature blob, its length and
// __LINKEDIT sizes changed by codesign are excluded. No executable code is skipped.
export function libraryFingerprint(input) {
  const data = Buffer.from(input);
  if (
    data.length < 32 ||
    data.readUInt32LE(0) !== 0xfeedfacf ||
    data.readUInt32LE(4) !== 0x100000c
  )
    throw new Error("Expected an arm64 Mach-O runtime");
  const count = data.readUInt32LE(16);
  const size = data.readUInt32LE(20);
  if (count < 1 || count > 256 || size > data.length - 32)
    throw new Error("Invalid runtime commands");
  let offset = 32;
  let cut = 0;
  let linkedit = 0;
  for (let i = 0; i < count; i++) {
    if (offset + 8 > 32 + size) throw new Error("Invalid runtime command");
    const cmd = data.readUInt32LE(offset);
    const length = data.readUInt32LE(offset + 4);
    if (length < 8 || offset + length > 32 + size)
      throw new Error("Invalid runtime length");
    if (cmd === 0x1d) {
      if (length !== 16 || cut) throw new Error("Invalid runtime signature");
      cut = data.readUInt32LE(offset + 8);
      const signatureSize = data.readUInt32LE(offset + 12);
      if (cut < 32 + size || signatureSize !== data.length - cut)
        throw new Error("Invalid signature bounds");
      data.fill(0, offset + 12, offset + 16);
    }
    if (
      cmd === 0x19 &&
      length >= 72 &&
      data
        .subarray(offset + 8, offset + 24)
        .toString()
        .replace(/\0+$/, "") === "__LINKEDIT"
    ) {
      linkedit = offset;
      data.fill(0, offset + 32, offset + 40);
      data.fill(0, offset + 48, offset + 56);
    }
    offset += length;
  }
  if (offset !== 32 + size || !cut || !linkedit)
    throw new Error("Incomplete runtime signature");
  return sha256(data.subarray(0, cut));
}
