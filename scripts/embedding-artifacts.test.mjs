import assert from "node:assert/strict";
import { test } from "node:test";
import { libraryFingerprint } from "./embedding-artifacts.mjs";
function fixture() {
  const data = Buffer.alloc(512);
  data.writeUInt32LE(0xfeedfacf, 0);
  data.writeUInt32LE(0x100000c, 4);
  data.writeUInt32LE(2, 16);
  data.writeUInt32LE(88, 20);
  data.writeUInt32LE(0x19, 32);
  data.writeUInt32LE(72, 36);
  data.write("__LINKEDIT", 40);
  data.writeUInt32LE(0x1d, 104);
  data.writeUInt32LE(16, 108);
  data.writeUInt32LE(400, 112);
  data.writeUInt32LE(112, 116);
  data[200] = 7;
  return data;
}
test("only signature data/length and linkedit sizes can change", () => {
  const expected = libraryFingerprint(fixture());
  const signed = Buffer.concat([fixture(), Buffer.alloc(50)]);
  signed.writeUInt32LE(162, 116);
  signed.writeBigUInt64LE(2048n, 64);
  signed.writeBigUInt64LE(1024n, 80);
  signed[450] = 42;
  assert.equal(libraryFingerprint(signed), expected);
  const changed = fixture();
  changed[200]++;
  assert.notEqual(libraryFingerprint(changed), expected);
  const shifted = fixture();
  shifted.writeUInt32LE(399, 112);
  shifted.writeUInt32LE(113, 116);
  assert.notEqual(libraryFingerprint(shifted), expected);
});
test("malformed or non-arm64 Mach-O files are rejected", () => {
  for (const data of [
    Buffer.alloc(0),
    Buffer.alloc(32),
    fixture().subarray(0, 200),
  ])
    assert.throws(() => libraryFingerprint(data));
});
