import assert from "node:assert/strict";
import test from "node:test";

import { webPAnimationOptions } from "./static/webp-options.mjs";

test("uses zero loop count for an infinitely looping WebP", () => {
  assert.deepEqual([...webPAnimationOptions(true)], [0, 0, 0, 0, 0, 0]);
});

test("uses one loop for a WebP that plays once", () => {
  assert.deepEqual([...webPAnimationOptions(false)], [0, 0, 0, 0, 1, 0]);
});
