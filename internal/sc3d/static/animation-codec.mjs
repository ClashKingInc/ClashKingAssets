const FLAG_FRAME_TIME = 1;
const FLAG_ROTATION = 2;
const FLAG_TRANSLATION = 4;
const FLAG_SCALE = 8;
const FLAG_SEPARATE_SCALE = 16;

function normalizeQuaternion(values) {
  const length = Math.hypot(values[0], values[1], values[2], values[3]) || 1;
  return values.map((value) => value / length);
}

function multiplyQuaternions(left, right) {
  return normalizeQuaternion([
    left[3] * right[0] + left[0] * right[3] + left[1] * right[2] - left[2] * right[1],
    left[3] * right[1] - left[0] * right[2] + left[1] * right[3] + left[2] * right[0],
    left[3] * right[2] + left[0] * right[1] - left[1] * right[0] + left[2] * right[3],
    left[3] * right[3] - left[0] * right[0] - left[1] * right[1] - left[2] * right[2]
  ]);
}

function inverseQuaternion(values) {
  const lengthSquared = values.reduce((sum, value) => sum + value * value, 0) || 1;
  return [
    -values[0] / lengthSquared,
    -values[1] / lengthSquared,
    -values[2] / lengthSquared,
    values[3] / lengthSquared
  ];
}

function validTransform(transform) {
  return transform &&
    transform.translation && transform.translation.length === 3 &&
    transform.rotation && transform.rotation.length === 4 &&
    transform.scale && transform.scale.length === 3;
}

// glTF animation channels replace a target node's local TRS. The geometry
// hierarchy remains authoritative; applying an additional rest-pose delta
// changes the authored motion and separates children from their attachments.
export function resolveAnimationLocalTransform(modelRest, animationSample) {
  const source = validTransform(animationSample) ? animationSample : modelRest;
  return {
    translation: source.translation.slice(),
    rotation: source.rotation.slice(),
    scale: source.scale.slice()
  };
}

const ANIMATION_SOURCE_ALIASES = new Map([
  ["rotorBlades_s", "rotorTop_s"],
  ["rotorRearBlades_s", "rotorRear_s"]
]);

export function resolveAnimationSourceName(modelNodeName) {
  return ANIMATION_SOURCE_ALIASES.get(modelNodeName) || modelNodeName;
}

// Some Supercell models animate a control node whose geometry equivalent has
// a different name and bind transform. Transfer only the authored rotation
// delta so the geometry node keeps its own pivot, translation, and scale.
export function resolveAliasedRotationTransform(modelRest, animationRest, animationSample) {
  if (!validTransform(modelRest) || !validTransform(animationRest) || !validTransform(animationSample)) {
    return resolveAnimationLocalTransform(modelRest, null);
  }
  const rotationDelta = multiplyQuaternions(
    inverseQuaternion(animationRest.rotation),
    animationSample.rotation
  );
  return {
    translation: modelRest.translation.slice(),
    rotation: multiplyQuaternions(modelRest.rotation, rotationDelta),
    scale: modelRest.scale.slice()
  };
}

export function requiresAnimationGlobalRemap(modelParentName, animationParentName) {
  return Boolean(
    modelParentName &&
    animationParentName &&
    modelParentName !== animationParentName
  );
}

function constantSample(base) {
  return {
    rotation: base.rotation.slice(),
    translation: base.translation.slice(),
    scale: base.scale.slice(),
    frame: 0
  };
}

function copySample(sample, frame) {
  return {
    rotation: sample.rotation.slice(),
    translation: sample.translation.slice(),
    scale: sample.scale.slice(),
    frame
  };
}

export function readContinuousPackedBases(nodeCount, nodeBaseData, baseRotationWords) {
  if (nodeBaseData.length < nodeCount * 8) {
    throw new Error(`packed animation base transform data is truncated (${nodeBaseData.length}/${nodeCount * 8})`);
  }
  if (baseRotationWords.length < nodeCount * 4) {
    throw new Error(`packed animation base rotation data is truncated (${baseRotationWords.length}/${nodeCount * 4})`);
  }

  return Array.from({ length: nodeCount }, (_, index) => {
    const transformOffset = index * 8;
    const rotationOffset = index * 4;
    return {
      translation: Array.from(nodeBaseData.slice(transformOffset, transformOffset + 3)),
      rotation: normalizeQuaternion(Array.from(baseRotationWords.slice(rotationOffset, rotationOffset + 4), (value) => value / 32767)),
      scale: Array.from(nodeBaseData.slice(transformOffset + 3, transformOffset + 6)),
      translationMultiplier: nodeBaseData[transformOffset + 6],
      scaleMultiplier: nodeBaseData[transformOffset + 7]
    };
  });
}

export function decodeContinuousPackedAnimation(packedNodes, dataWords, bases, fallbackFrameCount = 1) {
  if (packedNodes.length !== bases.length) {
    throw new Error(`packed animation node/base count mismatch (${packedNodes.length}/${bases.length})`);
  }

  const channels = [];
  let cursor = 0;
  let repeatBlockCount = 0;
  let explicitBlockCount = 0;

  for (let packedIndex = 0; packedIndex < packedNodes.length; packedIndex += 1) {
    const descriptor = packedNodes[packedIndex];
    const base = bases[packedIndex];
    const flags = descriptor.flags || 0;
    const frameCount = Math.max(1, Math.round(descriptor.frameCount || fallbackFrameCount));
    const dataSize = Math.max(0, Math.round(descriptor.dataSize || 0));
    const start = cursor;
    const end = start + dataSize;
    if (end > dataWords.length) {
      throw new Error(`packed animation node ${packedIndex} exceeds its data accessor (${end}/${dataWords.length})`);
    }

    if (!flags) {
      if (dataSize !== 0) throw new Error(`static packed animation node ${packedIndex} unexpectedly has ${dataSize} data words`);
      channels.push({ nodeIndex: descriptor.nodeIndex, samples: [constantSample(base)] });
      continue;
    }
    if (flags & FLAG_FRAME_TIME) {
      throw new Error(`packed animation node ${packedIndex} uses unsupported frame-time data`);
    }

    const samples = [];
    while (samples.length < frameCount) {
      if (samples.length > 0) {
        if (cursor >= end) throw new Error(`packed animation node ${packedIndex} is missing a repeat count`);
        const repeatCount = Math.abs(dataWords[cursor++]);
        const previous = samples[samples.length - 1];
        if (!previous && repeatCount) throw new Error(`packed animation node ${packedIndex} repeats before its first keyframe`);
        if (samples.length + repeatCount > frameCount) {
          throw new Error(`packed animation node ${packedIndex} repeats past frame ${frameCount}`);
        }
        for (let repeat = 0; repeat < repeatCount; repeat += 1) {
          samples.push(copySample(previous, samples.length));
        }
        if (repeatCount) repeatBlockCount += 1;
      }
      if (samples.length >= frameCount) break;

      if (cursor >= end) throw new Error(`packed animation node ${packedIndex} is missing a keyframe count`);
      const keyframeCount = dataWords[cursor++];
      if (keyframeCount <= 0 || samples.length + keyframeCount > frameCount) {
        throw new Error(`packed animation node ${packedIndex} has invalid keyframe count ${keyframeCount}`);
      }
      explicitBlockCount += 1;

      for (let keyframe = 0; keyframe < keyframeCount; keyframe += 1) {
        let rotation = base.rotation;
        let translation = base.translation;
        let scale = base.scale;

        if (flags & FLAG_ROTATION) {
          if (cursor + 4 > end) throw new Error(`packed animation node ${packedIndex} has a truncated rotation`);
          rotation = normalizeQuaternion(Array.from(dataWords.slice(cursor, cursor + 4), (value) => value / 32767));
          cursor += 4;
        }
        if (flags & FLAG_TRANSLATION) {
          if (cursor + 3 > end) throw new Error(`packed animation node ${packedIndex} has a truncated translation`);
          translation = [
            base.translation[0] + dataWords[cursor] * base.translationMultiplier,
            base.translation[1] + dataWords[cursor + 1] * base.translationMultiplier,
            base.translation[2] + dataWords[cursor + 2] * base.translationMultiplier
          ];
          cursor += 3;
        }
        if (flags & FLAG_SCALE) {
          const componentCount = flags & FLAG_SEPARATE_SCALE ? 3 : 1;
          if (cursor + componentCount > end) throw new Error(`packed animation node ${packedIndex} has a truncated scale`);
          if (componentCount === 1) {
            const delta = dataWords[cursor] * base.scaleMultiplier;
            scale = base.scale.map((value) => value + delta);
          } else {
            scale = [
              base.scale[0] + dataWords[cursor] * base.scaleMultiplier,
              base.scale[1] + dataWords[cursor + 1] * base.scaleMultiplier,
              base.scale[2] + dataWords[cursor + 2] * base.scaleMultiplier
            ];
          }
          cursor += componentCount;
        }

        samples.push({
          rotation: rotation.slice(),
          translation: translation.slice(),
          scale: scale.slice(),
          frame: samples.length
        });
      }
    }

    if (cursor !== end) {
      throw new Error(`packed animation node ${packedIndex} left ${end - cursor} unread data words`);
    }
    channels.push({ nodeIndex: descriptor.nodeIndex, samples });
  }

  if (cursor !== dataWords.length) {
    throw new Error(`packed animation left ${dataWords.length - cursor} unread data words`);
  }
  return { channels, stats: { repeatBlockCount, explicitBlockCount, consumedWordCount: cursor } };
}
