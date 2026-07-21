export function isPoseAnimationAsset(asset, selectedPath) {
  return asset !== selectedPath
    && asset.endsWith(".glb")
    && !asset.includes("_geo")
    && !asset.includes(".ingame.");
}
