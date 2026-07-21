import assert from "node:assert/strict";
import test from "node:test";

import { isPoseAnimationAsset } from "./static/pose-candidates.mjs";

test("accepts optimized and ordinary animation GLBs", () => {
  assert.equal(isPoseAnimationAsset("sc3d/battlecopter_hotrod_idle1_dl_opt.glb", "sc3d/battlecopter_hotrod_geo_dl_opt.glb"), true);
  assert.equal(isPoseAnimationAsset("sc3d/guardianmelee_default_idle1.glb", "sc3d/guardianmelee_default_geo.glb"), true);
});

test("rejects geometry and the selected model", () => {
  const selected = "sc3d/guardianmelee_default_geo.glb";
  assert.equal(isPoseAnimationAsset(selected, selected), false);
  assert.equal(isPoseAnimationAsset("sc3d/guardianmelee_default_geo.ingame.glb", selected), false);
  assert.equal(isPoseAnimationAsset("sc3d/guardianmelee_default_a.sctx", selected), false);
});
