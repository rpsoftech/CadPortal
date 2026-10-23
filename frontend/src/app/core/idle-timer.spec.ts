import { describe, expect, it } from 'vitest';
import { IdleEnv, IdleTimer, warnWindowMs } from './idle-timer';

/** A clock whose timers only run when the test advances time. */
function fakeEnv() {
  let now = 1_000_000;
  let stored: number | null = null;
  let seq = 0;
  const timers = new Map<number, { at: number; fn: () => void }>();
  const env: IdleEnv = {
    now: () => now,
    setTimeout: (fn, ms) => {
      timers.set(++seq, { at: now + ms, fn });
      return seq;
    },
    clearTimeout: (h) => void timers.delete(h as number),
    read: () => stored,
    write: (ms) => (stored = ms),
  };
  const advance = (ms: number) => {
    const end = now + ms;
    for (;;) {
      const due = [...timers.entries()]
        .filter(([, t]) => t.at <= end)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (!due) break;
      timers.delete(due[0]);
      now = Math.max(now, due[1].at);
      due[1].fn();
    }
    now = end;
  };
  return {
    env,
    advance,
    setShared: (v: number | null) => (stored = v),
    shared: () => stored,
    now: () => now,
  };
}

function setup(timeoutMs = 10 * 60_000) {
  const f = fakeEnv();
  const log: string[] = [];
  const t = new IdleTimer(timeoutMs, f.env, {
    warn: (left) => log.push(`warn ${Math.round(left / 1000)}`),
    tick: (left) => log.push(`tick ${Math.round(left / 1000)}`),
    resume: () => log.push('resume'),
    expire: () => log.push('expire'),
  });
  return { f, t, log };
}

describe('IdleTimer', () => {
  it('warns 60 s before the timeout and then expires', () => {
    const { f, t, log } = setup();
    t.start();
    f.advance(9 * 60_000 - 1000);
    expect(log).toEqual([]);
    f.advance(1500);
    expect(log[0]).toBe('warn 60');
    f.advance(60_000);
    expect(log.at(-1)).toBe('expire');
    expect(log.filter((l) => l === 'expire').length).toBe(1);
    expect(t.isRunning).toBe(false);
  });

  it('ticks about once a second while the warning is up', () => {
    const { f, t, log } = setup();
    t.start();
    f.advance(9 * 60_000 + 5000);
    expect(log.filter((l) => l.startsWith('tick')).length).toBeGreaterThanOrEqual(3);
    expect(log.at(-1)).toMatch(/^tick (5[4-6])$/);
  });

  it('activity postpones the warning', () => {
    const { f, t, log } = setup();
    t.start();
    f.advance(8 * 60_000);
    t.activity();
    f.advance(8 * 60_000);
    expect(log).toEqual([]);
    f.advance(2 * 60_000);
    expect(log[0]).toBe('warn 60');
  });

  it('activity during the warning is ignored; only stay() answers it', () => {
    const { f, t, log } = setup();
    t.start();
    f.advance(9 * 60_000 + 2000);
    expect(t.isWarning).toBe(true);
    t.activity();
    f.advance(60_000);
    expect(log.at(-1)).toBe('expire');

    const b = setup();
    b.t.start();
    b.f.advance(9 * 60_000 + 2000);
    b.t.stay();
    expect(b.log.at(-1)).toBe('resume');
    b.f.advance(8 * 60_000);
    expect(b.log.filter((l) => l === 'expire')).toEqual([]);
    b.f.advance(90_000);
    expect(b.log.at(-1)).toMatch(/^(warn|tick)/);
  });

  it('activity in another tab (shared time) keeps this tab signed in and clears a warning', () => {
    const { f, t, log } = setup();
    t.start();
    f.advance(8 * 60_000);
    f.setShared(f.now()); // the other tab was used just now
    f.advance(1.5 * 60_000);
    expect(log).toEqual([]); // 9.5 minutes after start, but only 1.5 after the other tab's activity
    f.advance(8 * 60_000);
    expect(log[0]).toMatch(/^warn/);
    // Another tab's activity during our warning dismisses it.
    f.setShared(f.now());
    t.check();
    expect(log.at(-1)).toBe('resume');
    expect(t.isWarning).toBe(false);
  });

  it('works without shared storage (read always null)', () => {
    const f = fakeEnv();
    const log: string[] = [];
    const t = new IdleTimer(
      120_000,
      { ...f.env, read: () => null, write: () => undefined },
      {
        warn: () => log.push('warn'),
        tick: () => undefined,
        resume: () => undefined,
        expire: () => log.push('expire'),
      },
    );
    t.start();
    f.advance(61_000);
    expect(log).toEqual(['warn']);
    f.advance(60_000);
    expect(log).toEqual(['warn', 'expire']);
  });

  it('expires at once when the computer slept past the timeout', () => {
    const { f, t, log } = setup();
    t.start();
    f.advance(60 * 60_000);
    expect(log.at(-1)).toBe('expire');
    expect(log.filter((l) => l === 'expire').length).toBe(1);
  });

  it('writes shared time at most every couple of seconds', () => {
    const { f, t } = setup();
    t.start();
    const first = f.shared();
    f.advance(500);
    t.activity();
    expect(f.shared()).toBe(first);
    f.advance(2500);
    t.activity();
    expect(f.shared()).toBe(f.now());
  });

  it('a short timeout warns for at most half of it', () => {
    expect(warnWindowMs(30 * 60_000)).toBe(60_000);
    expect(warnWindowMs(60_000)).toBe(30_000);
    expect(warnWindowMs(6000)).toBe(3000);
    const { f, t, log } = setup(6000);
    t.start();
    f.advance(3100);
    expect(log[0]).toBe('warn 3');
    f.advance(3000);
    expect(log.at(-1)).toBe('expire');
  });

  it('stop() cancels everything', () => {
    const { f, t, log } = setup();
    t.start();
    t.stop();
    f.advance(60 * 60_000);
    expect(log).toEqual([]);
  });
});
