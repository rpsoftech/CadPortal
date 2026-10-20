import { HttpClient, HttpEvent, HttpParams, HttpRequest } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import {
  BaseInput,
  BomResult,
  Category,
  CostingItem,
  DesignDetail,
  DesignFilter,
  DesignListItem,
  DownloadLink,
  Metal,
  Seat,
  SeatInput,
  StoneCode,
  Tag,
  UploadResult,
  User,
  UserRole,
  Variation,
} from './models';

const BASE = '/api/v1';

@Injectable({ providedIn: 'root' })
export class ApiService {
  private readonly http = inject(HttpClient);

  // ----- users (admin) -----
  users(): Observable<User[]> {
    return this.http.get<User[]>(`${BASE}/users`);
  }
  createUser(body: { email: string; name: string; password: string; role: UserRole }) {
    return this.http.post<User>(`${BASE}/users`, body);
  }
  updateUser(id: number, body: { name: string; role: UserRole; active: boolean }) {
    return this.http.put<User>(`${BASE}/users/${id}`, body);
  }
  resetUserPassword(id: number, newPassword: string) {
    return this.http.post<void>(`${BASE}/users/${id}/password`, { new_password: newPassword });
  }

  // ----- masters -----
  categories(): Observable<Category[]> {
    return this.http.get<Category[]>(`${BASE}/masters/categories`);
  }
  tags(): Observable<Tag[]> {
    return this.http.get<Tag[]>(`${BASE}/masters/tags`);
  }
  metals(): Observable<Metal[]> {
    return this.http.get<Metal[]>(`${BASE}/masters/metals`);
  }
  stoneCodes(): Observable<StoneCode[]> {
    return this.http.get<StoneCode[]>(`${BASE}/masters/stone-codes`);
  }

  // ----- designs -----
  listDesigns(f: DesignFilter): Observable<DesignListItem[]> {
    return this.http.get<DesignListItem[]>(`${BASE}/designs`, { params: designParams(f) });
  }
  getDesign(code: string): Observable<DesignDetail> {
    return this.http.get<DesignDetail>(`${BASE}/designs/${encodeURIComponent(code)}`);
  }
  createDesign(body: { code: string; name: string; category_id: number; tag_ids: number[] }) {
    return this.http.post<{ id: number; code: string }>(`${BASE}/designs`, body);
  }

  // ----- variations -----
  createVariation(designId: number, base: BaseInput): Observable<Variation> {
    return this.http.post<Variation>(`${BASE}/designs/${designId}/variations`, base);
  }
  updateBaseInput(variationId: number, base: BaseInput): Observable<Variation> {
    return this.http.put<Variation>(`${BASE}/variations/${variationId}/base-input`, base);
  }

  // ----- seats and BOM -----
  seats(variationId: number): Observable<Seat[]> {
    return this.http.get<Seat[]>(`${BASE}/variations/${variationId}/seats`);
  }
  setSeats(variationId: number, seats: SeatInput[]): Observable<Variation> {
    const body = {
      seats: seats.map((s) => ({
        size_mm: s.size_mm,
        stone_count: s.stone_count,
        locked_stone_code: s.locked_stone_code,
      })),
    };
    return this.http.put<Variation>(`${BASE}/variations/${variationId}/seats`, body);
  }
  cloneSeats(targetId: number, sourceVariationId: number): Observable<Variation> {
    return this.http.post<Variation>(`${BASE}/variations/${targetId}/clone-seats`, {
      source_variation_id: sourceVariationId,
    });
  }
  simulateBom(variationId: number, code: string): Observable<BomResult> {
    const params = new HttpParams().set('code', code);
    return this.http.get<BomResult>(`${BASE}/variations/${variationId}/simulate-bom`, { params });
  }

  // ----- CAD vault -----
  /** Streams a multipart upload; emits progress events, then the final response. */
  uploadCad(variationId: number, file: File): Observable<HttpEvent<UploadResult>> {
    const form = new FormData();
    form.append('file', file, file.name);
    return this.http.request<UploadResult>(
      new HttpRequest('POST', `${BASE}/variations/${variationId}/upload-cad`, form, {
        reportProgress: true,
      }),
    );
  }
  downloadLink(variationId: number, kind: '3dm' | 'stl'): Observable<DownloadLink> {
    const params = new HttpParams().set('kind', kind);
    return this.http.get<DownloadLink>(`${BASE}/variations/${variationId}/download`, { params });
  }

  // ----- exports (binary responses) -----
  jobBagPdf(body: {
    variation_id: number;
    metal_code: string;
    stone_code: string;
  }): Observable<Blob> {
    return this.http.post(`${BASE}/exports/job-bag-pdf`, body, { responseType: 'blob' });
  }
  costingCsv(body: {
    items: CostingItem[];
    gold_rate_per_g: string;
    setting_rate_per_pc: string;
  }): Observable<Blob> {
    return this.http.post(`${BASE}/exports/costing-csv`, body, { responseType: 'blob' });
  }
}

/** Maps the catalog filter to the API's query parameters, dropping empty values. */
export function designParams(f: DesignFilter): HttpParams {
  let p = new HttpParams();
  const set = (k: string, v: string | number | null | undefined) => {
    if (v !== null && v !== undefined && `${v}`.trim() !== '') p = p.set(k, `${v}`.trim());
  };
  set('category_id', f.categoryId);
  set('metal_code', f.metalCode);
  set('min_net_wt', f.minNetWt);
  set('max_net_wt', f.maxNetWt);
  set('min_gross_wt', f.minGrossWt);
  set('max_gross_wt', f.maxGrossWt);
  set('max_stone_cts', f.maxStoneCts);
  for (const id of f.tagIds ?? []) p = p.append('tag_ids[]', `${id}`);
  return p;
}
