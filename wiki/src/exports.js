import { assetUrl, costParts, duration, hallLabel, number, resourceArt, villageArt } from './catalog.js';
import { createAssetArchive } from './archive.js';

export function saveBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
}

export async function copyImage(getImage) {
  if (!navigator.clipboard?.write || typeof ClipboardItem !== 'function') throw new Error('Image clipboard unavailable');
  // Safari requires starting the clipboard operation in the original click.
  return navigator.clipboard.write([new ClipboardItem({ 'image/png': getImage() })]);
}

export async function downloadAssets(files, filename) {
  const data = await createAssetArchive(files, async (path) => {
    const response = await fetch(assetUrl(path));
    if (!response.ok) throw new Error('Unable to download ' + path);
    return new Uint8Array(await response.arrayBuffer());
  });
  saveBlob(new Blob([data], { type: 'application/zip' }), filename);
}

export async function downloadAsset(path) {
  const response = await fetch(assetUrl(path));
  if (!response.ok) throw new Error('Unable to download asset');
  saveBlob(await response.blob(), path.split('/').at(-1));
}

function loadArt(path) {
  if (!path) return Promise.resolve(null);
  return new Promise((resolve) => {
    const image = new Image();
    image.crossOrigin = 'anonymous';
    image.onload = () => resolve(image);
    image.onerror = () => resolve(null);
    image.src = assetUrl(path);
  });
}

function drawImage(context, image, centerX, centerY, bounds) {
  if (!image) return;
  const scale = Math.min(1.5, bounds / image.width, bounds / image.height);
  const width = image.width * scale;
  const height = image.height * scale;
  context.drawImage(image, centerX - width / 2, centerY - height / 2, width, height);
}

export function pngBlob(canvas) {
  return new Promise((resolve, reject) => canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error('Unable to create PNG')), 'image/png'));
}

export async function progressionPoster(entity, entries) {
  await Promise.all([document.fonts.load('38px SCMagic'), document.fonts.load('600 20px ClashKing')]);
  const withArt = entries.some((entry) => entry.art);
  const [portrait, tiles] = await Promise.all([
    !withArt && loadArt(entity.art?.path),
    Promise.all(entries.map(async (entry) => {
      const values = await Promise.all(costParts(entry.step?.cost, entry.step?.resource)
        .map(async ([name, amount]) => [name, amount, await loadArt(resourceArt[name])]));
      const [image, hall, unlockIcon] = await Promise.all([
        loadArt(entry.art?.path), loadArt(villageArt(entity.village, entry.hall)), loadArt(entry.unlock?.art?.path),
      ]);
      return { entry, values, image, hall, unlockIcon };
    })),
  ]);
  const count = entries.length;
  const columns = count <= 6 ? Math.min(count, 3) : count >= 17 ? 5 : 4;
  const rows = Math.ceil(count / columns);
  const gap = 15;
  const padding = 28;
  const bannerTop = 22;
  const bannerHeight = 68;
  const bannerCenter = bannerTop + bannerHeight / 2;
  const headerHeight = bannerTop + bannerHeight + 20;
  const withUnlocks = entries.some((entry) => entry.unlock);
  const canvas = document.createElement('canvas');
  canvas.width = columns === 5 ? 1280 : 1080;
  const cardWidth = (canvas.width - padding * 2 - gap * (columns - 1)) / columns;
  const ctx = canvas.getContext('2d');
  const costFont = '600 18px ClashKing, sans-serif';
  const detailGap = 14;
  const maxDetailsWidth = cardWidth - 24;
  const details = tiles.map(({ entry, values }) => {
    const time = Number.isFinite(entry.step?.seconds) ? duration(entry.step.seconds) : '';
    const [resource, amount, icon] = values[0] || [];
    const cost = values.length === 1 ? number(amount) : '';
    ctx.font = costFont;
    const costTextWidth = ctx.measureText(cost).width;
    const iconWidth = icon && cost ? 23 : 0;
    const timeWidth = ctx.measureText(time).width;
    // Keep multiple resource labels and long values in separate rows when they need the width.
    const inlineTime = !!time && (!values.length || (cost && (icon || !resource)
      && costTextWidth + iconWidth + detailGap + timeWidth <= maxDetailsWidth));
    return { time, cost, costTextWidth, icon, iconWidth, timeWidth, inlineTime };
  });
  const stackedTime = details.some(({ time, inlineTime }) => time && !inlineTime);
  const cardHeight = (withArt ? 196 : 130) + (stackedTime ? 25 : 0) + (withUnlocks ? 30 : 0);
  canvas.height = headerHeight + padding + rows * (cardHeight + gap) + 30;
  const grass = ctx.createLinearGradient(0, 0, 0, canvas.height);
  grass.addColorStop(0, '#a0c763');
  grass.addColorStop(1, '#80a946');
  ctx.fillStyle = grass;
  ctx.fillRect(0, 0, canvas.width, canvas.height);

  // A subtle checker and grass blades evoke the game's field without a background illustration.
  ctx.fillStyle = '#f0ffba0e';
  for (let y = 0; y < canvas.height; y += 42) {
    for (let x = (Math.floor(y / 42) % 2) * 42; x < canvas.width; x += 84) ctx.fillRect(x, y, 42, 42);
  }
  ctx.fillStyle = '#26461328';
  for (let y = 21; y < canvas.height; y += 47) {
    for (let x = 12 + y % 71; x < canvas.width; x += 71) {
      ctx.beginPath();
      ctx.moveTo(x, y);
      ctx.lineTo(x + 5, y - 5);
      ctx.lineTo(x + 4, y);
      ctx.lineTo(x + 9, y - 4);
      ctx.lineTo(x + 7, y + 1);
      ctx.closePath();
      ctx.fill();
    }
  }

  ctx.fillStyle = '#19360f';
  ctx.beginPath();
  ctx.roundRect(padding, bannerTop, canvas.width - 2 * padding, bannerHeight, 19);
  ctx.fill();
  if (portrait) drawImage(ctx, portrait, padding + 51, bannerCenter, 52);
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  ctx.font = '38px SCMagic, sans-serif';
  ctx.fillStyle = '#fff7d9';
  ctx.fillText(entity.name, canvas.width / 2, bannerCenter, canvas.width - 2 * padding - (portrait ? 218 : 36));

  tiles.forEach(({ entry, values, image, hall, unlockIcon }, index) => {
    const row = Math.floor(index / columns);
    const inRow = Math.min(columns, count - row * columns);
    const x = (canvas.width - (inRow * (cardWidth + gap) - gap)) / 2 + index % columns * (cardWidth + gap);
    const y = headerHeight + row * (cardHeight + gap);
    const middle = x + cardWidth / 2;
    ctx.fillStyle = '#284712';
    ctx.beginPath();
    ctx.roundRect(x, y + 4, cardWidth, cardHeight, 17);
    ctx.fill();
    ctx.fillStyle = '#fff8dc';
    ctx.beginPath();
    ctx.roundRect(x, y, cardWidth, cardHeight, 17);
    ctx.fill();

    ctx.font = '600 19px ClashKing, sans-serif';
    ctx.fillStyle = '#243712';
    ctx.fillText(entry.title, middle, y + 25);
    drawImage(ctx, image, middle, y + 86, 94);

    if (entry.hall) {
      const gate = hallLabel(entity.village, entry.hall);
      const gateY = y + (withArt ? 143 : 57);
      ctx.font = '600 17px ClashKing, sans-serif';
      if (hall) drawImage(ctx, hall, middle - ctx.measureText(gate).width / 2 - 5, gateY, 28);
      ctx.fillText(gate, middle + (hall ? 13 : 0), gateY);
    }

    const costY = y + (withArt ? 173 : 108);
    const { time, cost, costTextWidth, icon, iconWidth, timeWidth, inlineTime } = details[index];
    ctx.font = costFont;
    if (inlineTime) {
      const costWidth = cost ? costTextWidth + iconWidth + detailGap : 0;
      const left = middle - (costWidth + timeWidth) / 2;
      if (icon) drawImage(ctx, icon, left + 9, costY, 18);
      if (cost) ctx.fillText(cost, left + iconWidth + costTextWidth / 2, costY);
      ctx.fillStyle = '#465c32';
      ctx.fillText(time, left + costWidth + timeWidth / 2, costY, maxDetailsWidth);
    } else {
      const lane = (cardWidth - 16) / Math.max(1, values.length);
      for (const [i, [name, amount, resourceIcon]] of values.entries()) {
        const center = x + 8 + lane * (i + 0.5);
        const formatted = number(amount);
        if (values.length === 1 && resourceIcon) {
          drawImage(ctx, resourceIcon, center - ctx.measureText(formatted).width / 2 - 10, costY, 18);
          ctx.fillText(formatted, center + 8, costY, lane - 26);
        } else {
          if (resourceIcon) drawImage(ctx, resourceIcon, center, costY - 21, 18);
          else if (name) {
            ctx.font = '600 13px ClashKing, sans-serif';
            ctx.fillText(name, center, costY - 21, lane - 4);
            ctx.font = costFont;
          }
          ctx.fillText(formatted, center, costY, lane - 4);
        }
      }
      if (time) {
        ctx.fillStyle = '#465c32';
        ctx.fillText(time, middle, costY + 24, maxDetailsWidth);
      }
    }
    if (entry.unlock) {
      const name = entry.unlock.name;
      const unlockY = y + cardHeight - 21;
      const maxWidth = cardWidth - (unlockIcon ? 46 : 24);
      ctx.fillStyle = '#395911';
      ctx.font = '600 16px ClashKing, sans-serif';
      if (unlockIcon) drawImage(ctx, unlockIcon, middle - Math.min(ctx.measureText(name).width, maxWidth) / 2 - 3, unlockY, 22);
      ctx.fillText(name, middle + (unlockIcon ? 13 : 0), unlockY, maxWidth);
    }
  });
  ctx.fillStyle = '#19360f';
  ctx.font = '600 15px ClashKing, sans-serif';
  ctx.fillText('ClashKing Wiki · assets.clashk.ing', canvas.width / 2, canvas.height - 24);
  return pngBlob(canvas);
}
