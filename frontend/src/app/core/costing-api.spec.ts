import { HttpErrorResponse, provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { beforeEach, describe, expect, it } from 'vitest';
import { ApiService } from './api.service';
import { toApiError } from './errors';
import { CostSheetRequest } from './models';

const req: CostSheetRequest = {
  design_ids: [3, 4],
  metal_code: '18KR',
  stone_code: '2HD',
  gold_rate_per_g: '6000',
  setting_rate_per_pc: '25',
  skip_incomplete: false,
};

describe('costing endpoints', () => {
  let api: ApiService;
  let http: HttpTestingController;
  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    api = TestBed.inject(ApiService);
    http = TestBed.inject(HttpTestingController);
  });

  it('previews as JSON and downloads the CSV as a blob, with the same request', () => {
    api.costingPreview(req).subscribe();
    const p = http.expectOne('/api/v1/exports/costing-preview');
    expect(p.request.method).toBe('POST');
    expect(p.request.body).toEqual(req);
    p.flush({});

    api.costingSelectionCsv({ ...req, skip_incomplete: true }).subscribe();
    const c = http.expectOne('/api/v1/exports/costing-csv');
    expect(c.request.responseType).toBe('blob');
    expect(c.request.body.skip_incomplete).toBe(true);
    c.flush(new Blob(['x']));
  });

  it('saves a sheet with its name and lists them with the total from X-Total-Count', () => {
    api.createCostingSheet({ ...req, name: 'Autumn' }).subscribe();
    const c = http.expectOne('/api/v1/costing-sheets');
    expect(c.request.body.name).toBe('Autumn');
    c.flush({});

    let got: { items: unknown[]; total: number } | undefined;
    api.costingSheets(25, 50).subscribe((r) => (got = r));
    const l = http.expectOne((r) => r.url === '/api/v1/costing-sheets' && r.method === 'GET');
    expect(l.request.params.get('limit')).toBe('25');
    expect(l.request.params.get('offset')).toBe('50');
    l.flush([{ id: 1 }], { headers: { 'X-Total-Count': '61' } });
    expect(got).toEqual({ items: [{ id: 1 }], total: 61 });
  });

  it('reads, downloads, deletes and re-costs a sheet by id', () => {
    api.costingSheet(7).subscribe();
    http.expectOne('/api/v1/costing-sheets/7').flush({});
    api.costingSheetCsv(7).subscribe();
    const c = http.expectOne('/api/v1/costing-sheets/7/csv');
    expect(c.request.responseType).toBe('blob');
    c.flush(new Blob(['x']));
    api.deleteCostingSheet(7).subscribe();
    const d = http.expectOne('/api/v1/costing-sheets/7');
    expect(d.request.method).toBe('DELETE');
    d.flush(null);
    api.recostCostingSheet(7, { gold_rate_per_g: '7000' }).subscribe();
    const r = http.expectOne('/api/v1/costing-sheets/7/recost');
    expect(r.request.method).toBe('POST');
    expect(r.request.body).toEqual({ gold_rate_per_g: '7000' });
    r.flush({});
  });
});

describe('toApiError with grouped costing problems', () => {
  it('keeps the designs and variations the API says are incomplete', async () => {
    const problems = [
      {
        design_id: 1,
        design_code: 'LR0001',
        design_name: '',
        variations: [{ variation_id: 4, variation_code: 'LR0001-V1', message: 'price not set' }],
      },
    ];
    const blob = new Blob([JSON.stringify({ error: 'cannot cost: data is missing', problems })]);
    const e = await toApiError(new HttpErrorResponse({ status: 422, error: blob }));
    expect(e.problems).toEqual(problems);
    expect(e.message).toContain('cannot cost');
  });
});
