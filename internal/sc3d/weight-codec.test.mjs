import assert from "node:assert/strict";
import test from "node:test";

import {
  NORMALIZED_WEIGHT_DENOMINATOR,
  decodeNormalizedWeightVector
} from "./static/weight-codec.mjs";

function assertWeights(packed, numerators) {
  const actual = new Float32Array(4);
  assert.equal(decodeNormalizedWeightVector(packed, actual), true);
  const expected = numerators.map((value) => value / NORMALIZED_WEIGHT_DENOMINATOR);
  for (let index = 0; index < expected.length; index += 1) {
    assert.ok(Math.abs(actual[index] - expected[index]) < 1e-7, `${actual[index]} != ${expected[index]}`);
  }
  assert.ok(Math.abs(actual.reduce((sum, value) => sum + value, 0) - 1) < 1e-7);
}

test("zero packed value assigns the vertex to its first joint", () => {
  assertWeights(0, [4095, 0, 0, 0]);
});

test("format 36 decodes its 11/11/10 fields on one shared scale", () => {
  const numerators = [1024, 1536, 1023, 512];
  const packed = ((numerators[1] << 21) | (numerators[2] << 10) | numerators[3]) >>> 0;
  assertWeights(packed, numerators);
});

test("the two high bits remain part of the second weight", () => {
  assertWeights(0xf5200000, [2134, 1961, 0, 0]);
});

test("a reported Battle Copter vertex keeps all four influences", () => {
  assertWeights(0xcdc4bc9c, [1990, 1646, 303, 156]);
});

test("malformed vectors are reported and never emit a negative weight", () => {
  const target = new Float32Array(4);
  assert.equal(decodeNormalizedWeightVector(0xffffffff, target), false);
  assert.equal(target[0], 0);
});
