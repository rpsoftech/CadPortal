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
      rate_per_pc: '100',
      rate_per_ct: null,
      rate_basis: 'pc' as const,
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
    expect(results).toEqual({
      tags: [],
      matrix: [],
      seats: [],
      designs: { items: [], total: 0 },
    });
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

  it('reads the total match count from X-Total-Count for paging', () => {
    let page: { items: { code: string; tag_names: string[] }[]; total: number } | undefined;
    api
      .listDesigns({ q: ' ring ', limit: 12, offset: 24 })
      .subscribe((p) => (page = p as typeof page));
    const req = http.expectOne((r) => r.url === '/api/v1/designs');
    expect(req.request.params.get('q')).toBe('ring');
    expect(req.request.params.get('limit')).toBe('12');
    expect(req.request.params.get('offset')).toBe('24');
    req.flush([{ code: 'LR025', tag_names: null }], { headers: { 'X-Total-Count': '57' } });
    expect(page?.total).toBe(57);
    expect(page?.items[0].tag_names).toEqual([]);
  });

  it('falls back to the page size when the total header is missing or invalid', () => {
    const totals: number[] = [];
    api.listDesigns({}).subscribe((p) => totals.push(p.total));
    http.expectOne((r) => r.url === '/api/v1/designs').flush([{ code: 'A' }, { code: 'B' }]);
    api.listDesigns({}).subscribe((p) => totals.push(p.total));
    http
      .expectOne((r) => r.url === '/api/v1/designs')
      .flush([{ code: 'A' }], { headers: { 'X-Total-Count': 'nope' } });
    expect(totals).toEqual([2, 1]);
  });

  it('updates and deletes designs and variations', () => {
    api.updateDesign(4, { name: 'N', category_id: 2, tag_ids: [1, 3] }).subscribe();
    const put = http.expectOne('/api/v1/designs/4');
    expect(put.request.method).toBe('PUT');
    expect(put.request.body).toEqual({ name: 'N', category_id: 2, tag_ids: [1, 3] });
    api.deleteDesign(4).subscribe();
    expect(http.expectOne('/api/v1/designs/4').request.method).toBe('DELETE');
    api.deleteVariation(9).subscribe();
    expect(http.expectOne('/api/v1/variations/9').request.method).toBe('DELETE');
  });

  it('restores variations and duplicates designs', () => {
    api.restoreVariation(9).subscribe();
    const r = http.expectOne('/api/v1/variations/9/restore');
    expect(r.request.method).toBe('POST');
    r.flush(null);
    api.duplicateDesign(4, { name: 'X', variation_ids: [1, 2], copy_tags: false }).subscribe();
    const d = http.expectOne('/api/v1/designs/4/duplicate');
    expect(d.request.method).toBe('POST');
    expect(d.request.body).toEqual({ name: 'X', variation_ids: [1, 2], copy_tags: false });
  });

  it('lists the audit log with filters and reads the total from the header', () => {
    let page: { items: unknown[]; total: number } | undefined;
    api
      .audit({
        action: 'metal.',
        userId: 4,
        from: '2026-10-01',
        to: '2026-10-31',
        limit: 50,
        offset: 100,
      })
      .subscribe((p) => (page = p));
    const req = http.expectOne((r) => r.url === '/api/v1/audit');
    expect(req.request.params.get('action')).toBe('metal.');
    expect(req.request.params.get('user_id')).toBe('4');
    expect(req.request.params.get('from')).toBe('2026-10-01');
    expect(req.request.params.get('to')).toBe('2026-10-31');
    expect(req.request.params.get('limit')).toBe('50');
    expect(req.request.params.get('offset')).toBe('100');
    req.flush([{ id: 1 }], { headers: { 'X-Total-Count': '321' } });
    expect(page?.total).toBe(321);
    expect(page?.items).toHaveLength(1);

    // Empty filters send no parameters; a missing body is an empty list.
    api.audit({ action: '', userId: null, from: '', to: '' }).subscribe((p) => (page = p));
    const bare = http.expectOne((r) => r.url === '/api/v1/audit');
    expect(bare.request.params.keys()).toEqual([]);
    bare.flush(null);
    expect(page).toEqual({ items: [], total: 0 });
  });
});
