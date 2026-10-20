import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RecalcStatus } from '../../core/models';
import { RecalcWatcher } from './recalc-watcher';

const status = (pending: number, running = 0, failed = 0, last_error = ''): RecalcStatus => ({
  pending,
  running,
  failed,
  last_error,
});

describe('RecalcWatcher', () => {
  let ctl: HttpTestingController;
  let w: RecalcWatcher;

  beforeEach(() => {
    vi.useFakeTimers();
    TestBed.configureTestingModule({
      providers: [RecalcWatcher, provideHttpClient(), provideHttpClientTesting()],
    });
    ctl = TestBed.inject(HttpTestingController);
    w = TestBed.inject(RecalcWatcher);
  });
  afterEach(() => vi.useRealTimers());

  const next = () => ctl.expectOne('/api/v1/masters/recalc-status');

  it('loads the status on creation and reports idle', () => {
    next().flush(status(0));
    expect(w.status()).toEqual(status(0));
    expect(w.busy()).toBe(false);
    expect(w.failedMessage()).toBe('');
  });

  it('is busy while jobs are pending or running', () => {
    next().flush(status(2, 1));
    expect(w.busy()).toBe(true);
    w.refresh();
    next().flush(status(0, 1));
    expect(w.busy()).toBe(true);
  });

  it('polls until the queue drains, then signals settled once', () => {
    next().flush(status(0));
    const before = w.settled();
    w.watch();
    next().flush(status(1)); // immediate refresh shows "queued"
    expect(w.busy()).toBe(true);

    vi.advanceTimersByTime(1500);
    next().flush(status(1, 1)); // still working
    expect(w.settled()).toBe(before);

    vi.advanceTimersByTime(1500);
    next().flush(status(0)); // drained
    expect(w.busy()).toBe(false);
    expect(w.settled()).toBe(before + 1);

    vi.advanceTimersByTime(10_000); // polling has stopped
    ctl.expectNone('/api/v1/masters/recalc-status');
  });

  it('surfaces a failed recalculation with its message', () => {
    next().flush(status(0, 0, 1, 'variation 4: boom'));
    expect(w.failedMessage()).toBe('variation 4: boom');
  });

  it('stops polling if the status request fails', () => {
    next().flush(status(0));
    w.watch();
    next().flush(status(1));
    vi.advanceTimersByTime(1500);
    next().flush({ error: 'x' }, { status: 500, statusText: 'Server Error' });
    vi.advanceTimersByTime(10_000);
    ctl.expectNone('/api/v1/masters/recalc-status');
  });
});
