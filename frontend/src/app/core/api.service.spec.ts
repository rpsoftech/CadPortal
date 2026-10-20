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
});
