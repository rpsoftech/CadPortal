import { describe, expect, it } from 'vitest';
import {
  MAX_IMAGE_BYTES,
  MAX_MEDIA_PER_TARGET,
  MAX_VIDEO_BYTES,
  checkMediaFile,
  formatBytes,
  slotsLeft,
} from './media';

describe('checkMediaFile', () => {
  it.each([
    ['ring.jpg', 'image'],
    ['RING.JPEG', 'image'],
    ['front.png', 'image'],
    ['a.b.c.webp', 'image'],
    ['turntable.mp4', 'video'],
    ['clip.MOV', 'video'],
    ['clip.webm', 'video'],
  ])('%s is a %s', (name, kind) => {
    expect(checkMediaFile({ name, size: 1000 })).toEqual({ ok: true, kind });
  });

  it.each(['notes.pdf', 'anim.gif', 'photo.heic', 'model.3dm', 'noextension', 'photo.jpg.exe'])(
    '%s is refused with the allowed types named',
    (name) => {
      const r = checkMediaFile({ name, size: 1000 });
      expect(r.ok).toBe(false);
      if (!r.ok)
        expect(r.error).toContain('use JPEG, PNG or WebP photos, or MP4, MOV or WebM video');
    },
  );

  it('refuses an empty file and one over its limit, naming the limit', () => {
    expect(checkMediaFile({ name: 'a.jpg', size: 0 })).toEqual({
      ok: false,
      error: 'a.jpg is empty',
    });
    expect(checkMediaFile({ name: 'a.jpg', size: MAX_IMAGE_BYTES })).toEqual({
      ok: true,
      kind: 'image',
    });
    const photo = checkMediaFile({ name: 'a.jpg', size: MAX_IMAGE_BYTES + 1 });
    expect(photo.ok === false && photo.error).toContain('a photo can be at most 20 MB');
    const video = checkMediaFile({ name: 'a.mp4', size: MAX_VIDEO_BYTES + 1 });
    expect(video.ok === false && video.error).toContain('a video can be at most 300 MB');
    // a video may be much larger than a photo
    expect(checkMediaFile({ name: 'a.mp4', size: MAX_IMAGE_BYTES + 1 }).ok).toBe(true);
  });
});

describe('slotsLeft', () => {
  it.each([
    [0, MAX_MEDIA_PER_TARGET],
    [3, 2],
    [5, 0],
    [9, 0],
  ])('%i present leaves %i', (n, left) => expect(slotsLeft(n)).toBe(left));
});

describe('formatBytes', () => {
  it.each([
    [500, '500 B'],
    [2048, '2 KB'],
    [12_900_000, '12.3 MB'],
  ])('%i is %s', (n, s) => expect(formatBytes(n)).toBe(s));
});
