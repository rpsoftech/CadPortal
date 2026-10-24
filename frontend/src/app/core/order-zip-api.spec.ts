import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { beforeEach, describe, expect, it } from 'vitest';
import { ApiService } from './api.service';

describe('order download links', () => {
  let api: ApiService;
  let http: HttpTestingController;
  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    api = TestBed.inject(ApiService);
    http = TestBed.inject(HttpTestingController);
  });

  it('asks for the CAD ZIP of an order with the chosen kinds', () => {
    api.orderZipLink(4, ['stl', '3dm']).subscribe();
    const r = http.expectOne((x) => x.url === '/api/v1/orders/4/download/link');
    expect(r.request.method).toBe('GET');
    expect(r.request.params.get('files')).toBe('stl,3dm');
    r.flush({});
  });

  it('asks for the package on its own route', () => {
    api.orderZipLink(4, ['stl'], true).subscribe();
    const r = http.expectOne((x) => x.url === '/api/v1/orders/4/package/link');
    expect(r.request.params.get('files')).toBe('stl');
    r.flush({});
  });
});
