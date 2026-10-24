import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { beforeEach, describe, expect, it } from 'vitest';
import { ApiService } from './api.service';

describe('order endpoints', () => {
  let api: ApiService;
  let http: HttpTestingController;
  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    api = TestBed.inject(ApiService);
    http = TestBed.inject(HttpTestingController);
  });

  it('creates from a selection with a quantity each', () => {
    const body = { metal_code: '18KR', stone_code: '2HD', items: [{ variation_id: 7, quantity: 3 }] };
    api.createOrder(body).subscribe();
    const r = http.expectOne('/api/v1/orders');
    expect(r.request.method).toBe('POST');
    expect(r.request.body).toEqual(body);
    r.flush({});
  });

  it('lists with filters, dropping empty ones, and reads the total from the header', () => {
    let total = -1;
    api.orders({ status: 'placed', q: ' ', limit: 25, offset: 50 }).subscribe((r) => (total = r.total));
    const r = http.expectOne((x) => x.url === '/api/v1/orders');
    expect(r.request.params.get('status')).toBe('placed');
    expect(r.request.params.has('q')).toBe(false);
    expect(r.request.params.get('limit')).toBe('25');
    expect(r.request.params.get('offset')).toBe('50');
    r.flush([], { headers: { 'X-Total-Count': '81' } });
    expect(total).toBe(81);
  });

  it('edits, changes status, replaces lines and clones', () => {
    api.updateOrder(4, { name: 'A', notes: '', due_date: '2026-12-31' }).subscribe();
    http.expectOne({ method: 'PUT', url: '/api/v1/orders/4' }).flush({});
    api.setOrderLines(4, [{ variation_id: 1, quantity: 2 }]).subscribe();
    const l = http.expectOne({ method: 'PUT', url: '/api/v1/orders/4/lines' });
    expect(l.request.body).toEqual({ items: [{ variation_id: 1, quantity: 2 }] });
    l.flush({});
    api.setOrderStatus(4, 'placed', 'go').subscribe();
    const s = http.expectOne({ method: 'POST', url: '/api/v1/orders/4/status' });
    expect(s.request.body).toEqual({ status: 'placed', note: 'go' });
    s.flush({});
    api.cloneOrder(4, { skip_unavailable: true }).subscribe();
    const c = http.expectOne({ method: 'POST', url: '/api/v1/orders/4/clone' });
    expect(c.request.body).toEqual({ skip_unavailable: true });
    c.flush({});
    api.order(4).subscribe();
    http.expectOne({ method: 'GET', url: '/api/v1/orders/4' }).flush({});
    http.verify();
  });
});

describe('order live check', () => {
  it('reads how each line compares with the catalog', () => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    const api = TestBed.inject(ApiService);
    const http = TestBed.inject(HttpTestingController);
    let got: unknown = null;
    api.orderLive(4).subscribe((l) => (got = l));
    http.expectOne('/api/v1/orders/4/live').flush({ lines: [{ line_id: 9, status: 'changed', changes: [] }] });
    expect(got).toEqual([{ line_id: 9, status: 'changed', changes: [] }]);
  });
});
