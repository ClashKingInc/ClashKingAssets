import assert from "node:assert/strict";
import test from "node:test";

import {
  decodeContinuousPackedAnimation,
  readContinuousPackedBases,
  resolveAliasedRotationTransform,
  resolveAnimationLocalTransform,
  resolveAnimationSourceName,
  requiresAnimationGlobalRemap
} from "./static/animation-codec.mjs";

const identity = [0, 0, 0, 32767];

function base(overrides = {}) {
  return {
    translation: [10, 20, 30],
    rotation: [0, 0, 0, 1],
    scale: [1, 1, 1],
    translationMultiplier: 0.5,
    scaleMultiplier: 0.25,
    ...overrides
  };
}

test("reads the eight-float transform base and separate packed rotation", () => {
  const bases = readContinuousPackedBases(
    1,
    new Float32Array([10, 20, 30, 1, 2, 3, 0.5, 0.25]),
    new Int16Array(identity)
  );
  assert.deepEqual(bases[0].translation, [10, 20, 30]);
  assert.deepEqual(bases[0].rotation, [0, 0, 0, 1]);
  assert.deepEqual(bases[0].scale, [1, 2, 3]);
  assert.equal(bases[0].translationMultiplier, 0.5);
  assert.equal(bases[0].scaleMultiplier, 0.25);
});

test("decodes full continuous rotation and translation frames", () => {
  const descriptor = [{ nodeIndex: 7, flags: 6, frameCount: 2, dataSize: 15 }];
  const words = new Int16Array([2, ...identity, 2, 4, 6, ...identity, -2, -4, -6]);
  const decoded = decodeContinuousPackedAnimation(descriptor, words, [base()]);
  assert.deepEqual(decoded.channels[0].samples.map((sample) => sample.translation), [
    [11, 22, 33],
    [9, 18, 27]
  ]);
  assert.equal(decoded.stats.consumedWordCount, words.length);
});

test("expands repeat blocks before reading the next explicit block", () => {
  const quarterTurn = [0, 0, 23170, 23170];
  const halfTurn = [0, 0, 32767, 0];
  const words = new Int16Array([2, ...identity, ...quarterTurn, -2, 1, ...halfTurn]);
  const descriptor = [{ nodeIndex: 3, flags: 2, frameCount: 5, dataSize: words.length }];
  const samples = decodeContinuousPackedAnimation(descriptor, words, [base()]).channels[0].samples;
  assert.equal(samples.length, 5);
  assert.deepEqual(samples[2].rotation, samples[1].rotation);
  assert.deepEqual(samples[3].rotation, samples[1].rotation);
  assert.ok(Math.abs(samples[4].rotation[2] - 1) < 1e-7);
});

test("translation-only and static nodes retain their separate base rotations", () => {
  const rotation = [0, 0, Math.SQRT1_2, Math.SQRT1_2];
  const descriptors = [
    { nodeIndex: 1, flags: 4, frameCount: 1, dataSize: 4 },
    { nodeIndex: 2, flags: 0, frameCount: 1, dataSize: 0 }
  ];
  const decoded = decodeContinuousPackedAnimation(
    descriptors,
    new Int16Array([1, 2, 4, 6]),
    [base({ rotation }), base({ rotation })]
  );
  assert.deepEqual(decoded.channels[0].samples[0].rotation, rotation);
  assert.deepEqual(decoded.channels[1].samples[0].rotation, rotation);
});

test("rejects a node whose declared block leaves unread words", () => {
  assert.throws(
    () => decodeContinuousPackedAnimation(
      [{ nodeIndex: 1, flags: 2, frameCount: 1, dataSize: 6 }],
      new Int16Array([1, ...identity, 123]),
      [base()]
    ),
    /left 1 unread data words/
  );
});

test("animation samples replace geometry local transforms without a synthetic rest delta", () => {
  const modelRest = { translation: [100, 200, 300], rotation: [0, 0, 0, 1], scale: [2, 3, 4] };
  const animationSample = {
    translation: [-7.8, -2.9, -2.6],
    rotation: [-0.14, -0.06, 0.58, 0.8],
    scale: [1, 1, 1]
  };
  assert.deepEqual(resolveAnimationLocalTransform(modelRest, animationSample), animationSample);
});

test("nodes absent from an animation retain their geometry local transforms", () => {
  const modelRest = {
    translation: [3, 4, 5],
    rotation: [0, 0, Math.SQRT1_2, Math.SQRT1_2],
    scale: [2, 3, 4]
  };
  assert.deepEqual(resolveAnimationLocalTransform(modelRest, null), modelRest);
});

test("only parent-space mismatches require an animation-global remap", () => {
  assert.equal(requiresAnimationGlobalRemap("Root", "R_shoulder_s"), true);
  assert.equal(requiresAnimationGlobalRemap("R_clavicle_s", "R_clavicle_s"), false);
  assert.equal(requiresAnimationGlobalRemap("", "R_shoulder_s"), false);
});

test("maps steampunk rotor geometry to its authored control tracks", () => {
  assert.equal(resolveAnimationSourceName("rotorBlades_s"), "rotorTop_s");
  assert.equal(resolveAnimationSourceName("rotorRearBlades_s"), "rotorRear_s");
  assert.equal(resolveAnimationSourceName("mainBody_s"), "mainBody_s");
});

test("aliased animation transfers rotation delta without replacing the geometry pivot", () => {
  const quarterTurnX = [Math.SQRT1_2, 0, 0, Math.SQRT1_2];
  const quarterTurnY = [0, Math.SQRT1_2, 0, Math.SQRT1_2];
  const sourceSample = [0.5, 0.5, 0.5, 0.5]; // quarterTurnX * quarterTurnY
  const modelRest = { translation: [8, 9, 10], rotation: [0, 0, 0, 1], scale: [2, 3, 4] };
  const animationRest = { translation: [1, 2, 3], rotation: quarterTurnX, scale: [1, 1, 1] };
  const animationSample = { translation: [4, 5, 6], rotation: sourceSample, scale: [7, 8, 9] };

  const resolved = resolveAliasedRotationTransform(modelRest, animationRest, animationSample);
  assert.deepEqual(resolved.translation, modelRest.translation);
  assert.deepEqual(resolved.scale, modelRest.scale);
  resolved.rotation.forEach((value, index) => {
    assert.ok(Math.abs(value - quarterTurnY[index]) < 1e-7);
  });
});
