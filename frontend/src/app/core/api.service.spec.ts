import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { beforeEach, describe, expect, it } from 'vitest';
import { ApiService, designParams } from './api.service';

describe('designParams', () => {
  it('drops empty values and sends tags as tag_ids[]', () => {
    const p = designParams({
      categoryId: 3,
      metalCode: '18KR',
      minNetWt: '3',
      maxNetWt: '',
      minGrossWt: null,
      tagIds: [4, 9],
    });
    expect(p.get('category_id')).toBe('3');
    expect(p.get('metal_code')).toBe('18KR');
    expect(p.get('min_net_wt')).toBe('3');
    expect(p.has('max_net_wt')).toBe(false);
    expect(p.has('min_gross_wt')).toBe(false);
    expect(p.getAll('tag_ids[]')).toEqual(['4', '9']);
  });

  it('is empty for an empty filter', () => {
    expect(designParams({}).keys()).toEqual([]);
  });
});

describe('ApiService', () => {
  let api: ApiService;
  let http: HttpTestingController;
  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    api = TestBed.inject(ApiService);
    http = TestBed.inject(HttpTestingController);
  });

  it('sets seats with the lock code the API expects', () => {
    api.setSeats(7, [{ size_mm: '1.10', stone_count: 24, locked_stone_code: '' }]).subscribe();
    const req = http.expectOne('/api/v1/variations/7/seats');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({
      seats: [{ size_mm: '1.10', stone_count: 24, locked_stone_code: '' }],
    });
    req.flush({});
  });

  it('passes the stone code to simulate-bom and the kind to download', () => {
    api.simulateBom(2, '2HD').subscribe();
    expect(
      http.expectOne(
        (r) => r.url === '/api/v1/variations/2/simulate-bom' && r.params.get('code') === '2HD',
      ),
    ).toBeTruthy();
    api.downloadLink(2, 'stl').subscribe();
    expect(
      http.expectOne(
        (r) => r.url === '/api/v1/variations/2/download' && r.params.get('kind') === 'stl',
      ),
    ).toBeTruthy();
  });

  it('uploads as multipart with a "file" part', () => {
    api.uploadCad(5, new File(['x'], 'ring.3dm')).subscribe();
    const req = http.expectOne('/api/v1/variations/5/upload-cad');
    expect(req.request.method).toBe('POST');
    expect((req.request.body as FormData).get('file')).toBeInstanceOf(File);
    expect(req.request.reportProgress).toBe(true);
  });

  it('requests exports as blobs', () => {
    api.jobBagPdf({ variation_id: 1, metal_code: '18KR', stone_code: '2HD' }).subscribe();
    expect(http.expectOne('/api/v1/exports/job-bag-pdf').request.responseType).toBe('blob');
  });

  it('saves matrix rows and the baseline in one call', () => {
    const row = {
      size_mm: '1.10',
      stone_code: '2HD',
      sieve: '',
      carats_per_stone: '0.0070',
      rate_per_ct: null,
    };
    api.saveStoneMatrix([row], '2HD').subscribe();
    const req = http.expectOne('/api/v1/masters/stone-matrix');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ rows: [row], baseline_code: '2HD' });
    api.saveStoneMatrix([]).subscribe();
    expect(http.expectOne('/api/v1/masters/stone-matrix').request.body).toEqual({
      rows: [],
      baseline_code: '',
    });
  });

  it('deletes a matrix row by size and code', () => {
    api.deleteMatrixRow('1.10', '2HD').subscribe();
    const req = http.expectOne(
      (r) => r.url === '/api/v1/masters/stone-matrix' && r.method === 'DELETE',
    );
    expect(req.request.params.get('size_mm')).toBe('1.10');
    expect(req.request.params.get('stone_code')).toBe('2HD');
  });

  it('asks for deactivated tags only when told to', () => {
    api.tags().subscribe();
    expect(http.expectOne('/api/v1/masters/tags').request.params.has('include_inactive')).toBe(
      false,
    );
    api.tags(true).subscribe();
    expect(
      http
        .expectOne((r) => r.url === '/api/v1/masters/tags')
        .request.params.get('include_inactive'),
    ).toBe('true');
  });

  it('creates and updates alloys with a nullable wastage', () => {
    const body = {
      specific_gravity: '15.6',
      wastage_pct: null,
      is_default_input: false,
      active: true,
    };
    api.createMetal({ ...body, code: '18KW' }).subscribe();
    const c = http.expectOne('/api/v1/masters/metals');
    expect(c.request.method).toBe('POST');
    expect(c.request.body.wastage_pct).toBeNull();
    api.updateMetal(4, { ...body, wastage_pct: '9.5' }).subscribe();
    const u = http.expectOne('/api/v1/masters/metals/4');
    expect(u.request.method).toBe('PUT');
    expect(u.request.body.wastage_pct).toBe('9.5');
  });

  it('hits the stone-code, category and gap endpoints', () => {
    api.createStoneCode('3HD').subscribe();
    expect(http.expectOne('/api/v1/masters/stone-codes').request.body).toEqual({ code: '3HD' });
    api.setStoneCodeActive(3, false).subscribe();
    expect(http.expectOne('/api/v1/masters/stone-codes/3').request.body).toEqual({ active: false });
    api.updateCategory(2, { name: 'Rings', code_prefix: 'LR' }).subscribe();
    expect(http.expectOne('/api/v1/masters/categories/2').request.method).toBe('PUT');
    api.matrixGaps().subscribe();
    expect(http.expectOne('/api/v1/masters/matrix-gaps').request.method).toBe('GET');
  });

  it('turns null lists into empty arrays so screens never crash on a fresh database', () => {
    const results: Record<string, unknown> = {};
    api.tags().subscribe((v) => (results['tags'] = v));
    http.expectOne('/api/v1/masters/tags').flush(null);
    api.stoneMatrix().subscribe((v) => (results['matrix'] = v));
    http.expectOne('/api/v1/masters/stone-matrix').flush(null);
    api.seats(3).subscribe((v) => (results['seats'] = v));
    http.expectOne('/api/v1/variations/3/seats').flush(null);
    api.listDesigns({}).subscribe((v) => (results['designs'] = v));
    http.expectOne((r) => r.url === '/api/v1/designs').flush(null);
    expect(results).toEqual({ tags: [], matrix: [], seats: [], designs: [] });
  });

  it('normalises null tags, variations and weights on a design', () => {
    let design: { tags: unknown[]; variations: { weights: unknown[] }[] } | undefined;
    api.getDesign('LR001').subscribe((d) => (design = d));
    http
      .expectOne('/api/v1/designs/LR001')
      .flush({ id: 1, code: 'LR001', tags: null, variations: [{ id: 5, weights: null }] });
    expect(design?.tags).toEqual([]);
    expect(design?.variations[0].weights).toEqual([]);

    api.getDesign('LR002').subscribe((d) => (design = d));
    http
      .expectOne('/api/v1/designs/LR002')
      .flush({ id: 2, code: 'LR002', tags: null, variations: null });
    expect(design?.variations).toEqual([]);
  });
});
