export function webPAnimationOptions(loop) {
  const options = new Uint8Array(6);
  const loopCount = loop ? 0 : 1;
  options[4] = loopCount & 0xff;
  options[5] = loopCount >>> 8;
  return options;
}
