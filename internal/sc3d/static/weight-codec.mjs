export const NORMALIZED_WEIGHT_DENOMINATOR = 4095;

// Odin format 36 stores three explicit weights in 11/11/10 bits. All three
// share a 12-bit normalization denominator; the first weight is implicit.
export function decodeNormalizedWeightVector(packed, target = new Float32Array(4), offset = 0) {
  const second = packed >>> 21;
  const third = (packed >>> 10) & 0x7ff;
  const fourth = packed & 0x3ff;
  const first = NORMALIZED_WEIGHT_DENOMINATOR - second - third - fourth;

  target[offset] = Math.max(0, first) / NORMALIZED_WEIGHT_DENOMINATOR;
  target[offset + 1] = second / NORMALIZED_WEIGHT_DENOMINATOR;
  target[offset + 2] = third / NORMALIZED_WEIGHT_DENOMINATOR;
  target[offset + 3] = fourth / NORMALIZED_WEIGHT_DENOMINATOR;
  return first >= 0;
}
