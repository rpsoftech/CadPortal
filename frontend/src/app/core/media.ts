/** A design, or one of its variations, holds this many photos and videos together. */
export const MAX_MEDIA_PER_TARGET = 5;
export const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
export const MAX_VIDEO_BYTES = 300 * 1024 * 1024;

/** What the file picker offers. The server decides by the file's content; this only helps the user. */
export const MEDIA_ACCEPT =
  'image/jpeg,image/png,image/webp,video/mp4,video/webm,video/quicktime,.jpg,.jpeg,.png,.webp,.mp4,.webm,.mov';

export type MediaKind = 'image' | 'video';

const IMAGE_EXT = ['jpg', 'jpeg', 'png', 'webp'];
const VIDEO_EXT = ['mp4', 'webm', 'mov'];

export type MediaCheck = { ok: true; kind: MediaKind } | { ok: false; error: string };

function megabytes(bytes: number): string {
  return `${Math.round(bytes / (1024 * 1024))} MB`;
}

/**
 * Early check of a file before it is sent, so a wrong type or a huge file is refused at once and
 * not after a long upload. It goes by the extension (a browser may report no type for .mov) and
 * the size; the server still checks the real content and has the final say.
 */
export function checkMediaFile(f: { name: string; size: number }): MediaCheck {
  const ext = (f.name.split('.').pop() ?? '').toLowerCase();
  const kind: MediaKind | null = IMAGE_EXT.includes(ext)
    ? 'image'
    : VIDEO_EXT.includes(ext)
      ? 'video'
      : null;
  if (!kind) {
    return {
      ok: false,
      error: `${f.name}: use JPEG, PNG or WebP photos, or MP4, MOV or WebM video`,
    };
  }
  if (f.size <= 0) return { ok: false, error: `${f.name} is empty` };
  const limit = kind === 'image' ? MAX_IMAGE_BYTES : MAX_VIDEO_BYTES;
  if (f.size > limit) {
    return {
      ok: false,
      error: `${f.name} is too large: a ${kind === 'image' ? 'photo' : 'video'} can be at most ${megabytes(limit)}`,
    };
  }
  return { ok: true, kind };
}

/** How many more files fit, given how many are already there and how many are being added. */
export function slotsLeft(existing: number): number {
  return Math.max(0, MAX_MEDIA_PER_TARGET - existing);
}

/** A readable size: 850 KB, 12.4 MB. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}
