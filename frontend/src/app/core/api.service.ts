import { HttpClient, HttpEvent, HttpParams, HttpRequest, HttpResponse } from '@angular/common/http';
import { Injectable, inject, signal } from '@angular/core';
import { MonoTypeOperatorFunction, Observable, catchError, map, of, tap, throwError } from 'rxjs';
import {
  AuditFilter,
  AuditRecord,
  AuthConfig,
  ResetLink,
  Backups,
  BaseInput,
  BomResult,
  Category,
  CostingItem,
  CostingSheet,
  CostingSheetComparison,
  CostingSheetSummary,
  CostPreview,
  CostSheetRequest,
  CadZipLink,
  CadZipRequest,
  JobBagsRequest,
  DesignDetail,
  DuplicateDesignBody,
  DesignFilter,
  DesignImportResult,
  DesignImportRow,
  DesignListItem,
  DesignPage,
  MediaItem,
  Trash,
  DownloadLink,
  MatrixGap,
  MatrixRowInput,
  Metal,
  MetalInput,
  RecalcStatus,
  SetupStatus,
  Seat,
  SeatInput,
  StoneCode,
  StoneMatrixRow,
  Tag,
  TagGroup,
  UploadResult,
  User,
  UserRole,
  Variation,
} from './models';

const BASE = '/api/v1';

@Injectable({ providedIn: 'root' })
export class ApiService {
  private readonly http = inject(HttpClient);

  /**
   * Counts successful master-data writes, so anything derived from master data (the setup
   * checklist) can reload itself after the admin changes something, wherever they changed it.
   */
  readonly mastersChanged = signal(0);
  private touched<T>(): MonoTypeOperatorFunction<T> {
    return tap<T>(() => this.mastersChanged.update((n) => n + 1));
  }

  // ----- audit log (admin) -----
  audit(f: AuditFilter): Observable<{ items: AuditRecord[]; total: number }> {
    let params = new HttpParams();
    const set = (k: string, v: string | number | null | undefined) => {
      if (v !== null && v !== undefined && `${v}`.trim() !== '')
        params = params.set(k, `${v}`.trim());
    };
    set('action', f.action);
    set('user_id', f.userId);
    set('from', f.from);
    set('to', f.to);
    set('limit', f.limit);
    set('offset', f.offset);
    return this.http.get<AuditRecord[]>(`${BASE}/audit`, { params, observe: 'response' }).pipe(
      map((res) => {
        const items = res.body ?? [];
        const header = res.headers.get('X-Total-Count');
        const total = header === null ? NaN : Number(header);
        return { items, total: Number.isFinite(total) ? total : items.length };
      }),
    );
  }

  // ----- users (admin) -----
  users(): Observable<User[]> {
    return this.http.get<User[]>(`${BASE}/users`).pipe(orEmpty());
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

  /** An admin makes a one-hour link; the user chooses their own password with it. */
  userResetLink(id: number): Observable<ResetLink> {
    return this.http.post<ResetLink>(`${BASE}/users/${id}/reset-link`, null);
  }

  // ----- password reset (public: no sign-in needed) -----
  authConfig(): Observable<AuthConfig> {
    return this.http.get<AuthConfig>(`${BASE}/auth/config`);
  }
  forgotPassword(email: string): Observable<void> {
    return this.http.post<void>(`${BASE}/auth/forgot-password`, { email });
  }
  resetPasswordWithToken(token: string, password: string): Observable<void> {
    return this.http.post<void>(`${BASE}/auth/reset-password`, { token, password });
  }

  // ----- masters -----
  categories(): Observable<Category[]> {
    return this.http.get<Category[]>(`${BASE}/masters/categories`).pipe(orEmpty());
  }
  tags(includeInactive = false): Observable<Tag[]> {
    const params = includeInactive ? new HttpParams().set('include_inactive', 'true') : undefined;
    return this.http.get<Tag[]>(`${BASE}/masters/tags`, { params }).pipe(orEmpty());
  }
  createTag(body: { name: string; tag_group: TagGroup }): Observable<Tag> {
    return this.http.post<Tag>(`${BASE}/masters/tags`, body).pipe(this.touched());
  }
  updateTag(id: number, body: { name: string; tag_group: TagGroup; active: boolean }) {
    return this.http.put<Tag>(`${BASE}/masters/tags/${id}`, body).pipe(this.touched());
  }
  createCategory(body: { name: string; code_prefix: string }): Observable<Category> {
    return this.http.post<Category>(`${BASE}/masters/categories`, body).pipe(this.touched());
  }
  updateCategory(id: number, body: { name: string; code_prefix: string }): Observable<Category> {
    return this.http.put<Category>(`${BASE}/masters/categories/${id}`, body).pipe(this.touched());
  }
  createMetal(body: MetalInput & { code: string }): Observable<Metal> {
    return this.http.post<Metal>(`${BASE}/masters/metals`, body).pipe(this.touched());
  }
  updateMetal(id: number, body: MetalInput): Observable<Metal> {
    return this.http.put<Metal>(`${BASE}/masters/metals/${id}`, body).pipe(this.touched());
  }
  createStoneCode(code: string): Observable<StoneCode> {
    return this.http.post<StoneCode>(`${BASE}/masters/stone-codes`, { code }).pipe(this.touched());
  }
  setStoneCodeActive(id: number, active: boolean): Observable<StoneCode> {
    return this.http
      .put<StoneCode>(`${BASE}/masters/stone-codes/${id}`, { active })
      .pipe(this.touched());
  }
  stoneMatrix(): Observable<StoneMatrixRow[]> {
    return this.http.get<StoneMatrixRow[]>(`${BASE}/masters/stone-matrix`).pipe(orEmpty());
  }
  /** Upserts rows and/or moves the baseline code. Weights are rebuilt in the background. */
  saveStoneMatrix(rows: MatrixRowInput[], baselineCode = ''): Observable<unknown> {
    return this.http
      .put(`${BASE}/masters/stone-matrix`, { rows, baseline_code: baselineCode })
      .pipe(this.touched());
  }
  deleteMatrixRow(sizeMm: string, stoneCode: string): Observable<void> {
    const params = new HttpParams().set('size_mm', sizeMm).set('stone_code', stoneCode);
    return this.http.delete<void>(`${BASE}/masters/stone-matrix`, { params }).pipe(this.touched());
  }
  matrixGaps(): Observable<MatrixGap[]> {
    return this.http.get<MatrixGap[]>(`${BASE}/masters/matrix-gaps`).pipe(orEmpty());
  }
  setupStatus(): Observable<SetupStatus> {
    return this.http.get<SetupStatus>(`${BASE}/masters/setup-status`);
  }
  recalcStatus(): Observable<RecalcStatus> {
    return this.http.get<RecalcStatus>(`${BASE}/masters/recalc-status`);
  }
  metals(): Observable<Metal[]> {
    return this.http.get<Metal[]>(`${BASE}/masters/metals`).pipe(orEmpty());
  }
  stoneCodes(): Observable<StoneCode[]> {
    return this.http.get<StoneCode[]>(`${BASE}/masters/stone-codes`).pipe(orEmpty());
  }

  // ----- designs -----
  listDesigns(f: DesignFilter): Observable<DesignPage> {
    return this.http
      .get<DesignListItem[]>(`${BASE}/designs`, { params: designParams(f), observe: 'response' })
      .pipe(
        map((res) => {
          const items = (res.body ?? []).map((d) => ({ ...d, tag_names: d.tag_names ?? [] }));
          // X-Total-Count is the match count before paging; fall back to what we received.
          const header = res.headers.get('X-Total-Count');
          const total = header === null ? NaN : Number(header);
          return { items, total: Number.isFinite(total) ? total : items.length };
        }),
      );
  }
  getDesign(code: string): Observable<DesignDetail> {
    return this.http.get<DesignDetail>(`${BASE}/designs/${encodeURIComponent(code)}`).pipe(
      map((d) => ({
        ...d,
        tags: d.tags ?? [],
        variations: (d.variations ?? []).map((v) => ({ ...v, weights: v.weights ?? [] })),
      })),
    );
  }
  /** The design code is assigned by the server from the category prefix. */
  createDesign(body: { name: string; category_id: number; tag_ids: number[] }) {
    return this.http.post<{ id: number; code: string }>(`${BASE}/designs`, body);
  }
  /** A rejected import is a normal outcome (HTTP 422 with every problem), not an error. */
  importDesigns(rows: DesignImportRow[], dryRun: boolean): Observable<DesignImportResult> {
    return this.http
      .post<DesignImportResult>(`${BASE}/designs/import`, { rows, dry_run: dryRun })
      .pipe(
        catchError((e) =>
          e?.status === 422 && Array.isArray(e.error?.errors)
            ? of(e.error as DesignImportResult)
            : throwError(() => e),
        ),
      );
  }
  updateDesign(id: number, body: { name: string; category_id: number; tag_ids: number[] }) {
    return this.http.put<{ id: number; code: string }>(`${BASE}/designs/${id}`, body);
  }
  deleteDesign(id: number): Observable<void> {
    return this.http.delete<void>(`${BASE}/designs/${id}`);
  }
  deleteVariation(id: number): Observable<void> {
    return this.http.delete<void>(`${BASE}/variations/${id}`);
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
    return this.http.get<Seat[]>(`${BASE}/variations/${variationId}/seats`).pipe(orEmpty());
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
  removeCad(variationId: number, kind: '3dm' | 'stl'): Observable<void> {
    const params = new HttpParams().set('kind', kind);
    return this.http.delete<void>(`${BASE}/variations/${variationId}/cad`, { params });
  }
  // ----- backups (admin) -----
  backups(): Observable<Backups> {
    return this.http.get<Backups>(`${BASE}/backups`);
  }
  requestBackup(): Observable<void> {
    return this.http.post<void>(`${BASE}/backups/run`, null);
  }
  backupDownloadLink(key: string): Observable<DownloadLink> {
    return this.http.get<DownloadLink>(`${BASE}/backups/download`, { params: { key } });
  }

  // ----- undo: deleted designs and removed files -----
  trash(): Observable<Trash> {
    return this.http.get<Trash>(`${BASE}/trash`);
  }
  restoreFile(id: number): Observable<void> {
    return this.http.post<void>(`${BASE}/trash/files/${id}/restore`, null);
  }
  restoreDesign(id: number): Observable<void> {
    return this.http.post<void>(`${BASE}/designs/${id}/restore`, null);
  }
  restoreVariation(id: number): Observable<void> {
    return this.http.post<void>(`${BASE}/variations/${id}/restore`, null);
  }

  /** A new design (next automatic code) with copies of the chosen variations' base input and seats. */
  duplicateDesign(id: number, body: DuplicateDesignBody): Observable<DesignDetail> {
    return this.http.post<DesignDetail>(`${BASE}/designs/${id}/duplicate`, body);
  }

  // ----- photos and video -----
  /**
   * Streams a multipart upload of one photo or video. variation_id goes first: the server reads
   * the form as it arrives and needs it before the file.
   */
  uploadMedia(
    designId: number,
    file: File,
    variationId: number | null,
  ): Observable<HttpEvent<MediaItem>> {
    const form = new FormData();
    if (variationId !== null) form.append('variation_id', String(variationId));
    form.append('file', file, file.name);
    return this.http.request<MediaItem>(
      new HttpRequest('POST', `${BASE}/designs/${designId}/media`, form, { reportProgress: true }),
    );
  }
  deleteMedia(id: number): Observable<void> {
    return this.http.delete<void>(`${BASE}/media/${id}`);
  }
  setCover(id: number): Observable<void> {
    return this.http.put<void>(`${BASE}/media/${id}/cover`, null);
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
  /**
   * Several job bags in one PDF, in the given order (at most 200). Give `items`, or `design_ids` for
   * every variation of those designs, with a shared `metal_code` and `stone_code` as defaults.
   * With `skip_incomplete` the complete bags are printed and the `X-Skipped` response header lists the
   * variation codes left out; otherwise a bag with missing stone data fails the whole request (422).
   */
  jobBagsPdf(body: JobBagsRequest): Observable<HttpResponse<Blob>> {
    return this.http.post(`${BASE}/exports/job-bags-pdf`, body, {
      responseType: 'blob',
      observe: 'response',
    });
  }
  /** Checks a selection and returns what a ZIP of its CAD files holds, with a link to download it. */
  cadZipLink(body: CadZipRequest): Observable<CadZipLink> {
    return this.http.post<CadZipLink>(`${BASE}/downloads/cad-zip/link`, body);
  }
  /** Every master-data table as CSV files in one zip (admin only). */
  masterDataZip(): Observable<Blob> {
    return this.http.get(`${BASE}/masters/export`, { responseType: 'blob' });
  }
  designsCsv(): Observable<Blob> {
    return this.http.get(`${BASE}/exports/designs-csv`, { responseType: 'blob' });
  }
  costingCsv(body: {
    items: CostingItem[];
    gold_rate_per_g: string;
    setting_rate_per_pc: string;
  }): Observable<Blob> {
    return this.http.post(`${BASE}/exports/costing-csv`, body, { responseType: 'blob' });
  }

  // ----- costing several designs, and saved costing sheets (admin/designer) -----
  /** Costs a selection without failing on incomplete data: rows plus the problems, as JSON. */
  costingPreview(body: CostSheetRequest): Observable<CostPreview> {
    return this.http.post<CostPreview>(`${BASE}/exports/costing-preview`, body);
  }
  /** One CSV for the whole selection. Fails with 422 (and names what is missing) unless skip_incomplete. */
  costingSelectionCsv(body: CostSheetRequest): Observable<Blob> {
    return this.http.post(`${BASE}/exports/costing-csv`, body, { responseType: 'blob' });
  }
  createCostingSheet(body: CostSheetRequest & { name: string }): Observable<CostingSheet> {
    return this.http.post<CostingSheet>(`${BASE}/costing-sheets`, body);
  }
  costingSheets(limit: number, offset: number): Observable<{ items: CostingSheetSummary[]; total: number }> {
    const params = new HttpParams().set('limit', limit).set('offset', offset);
    return this.http
      .get<CostingSheetSummary[]>(`${BASE}/costing-sheets`, { params, observe: 'response' })
      .pipe(
        map((r) => ({
          items: r.body ?? [],
          total: Number(r.headers.get('X-Total-Count') ?? r.body?.length ?? 0),
        })),
      );
  }
  costingSheet(id: number): Observable<CostingSheet> {
    return this.http.get<CostingSheet>(`${BASE}/costing-sheets/${id}`);
  }
  /** The CSV exactly as it was made when the sheet was saved. */
  costingSheetCsv(id: number): Observable<Blob> {
    return this.http.get(`${BASE}/costing-sheets/${id}/csv`, { responseType: 'blob' });
  }
  deleteCostingSheet(id: number): Observable<void> {
    return this.http.delete<void>(`${BASE}/costing-sheets/${id}`);
  }
  /** A new sheet from the same variations with today's masters; rates left out stay as saved. */
  recostCostingSheet(
    id: number,
    body: { name?: string; gold_rate_per_g?: string; setting_rate_per_pc?: string; skip_incomplete?: boolean },
  ): Observable<{ sheet: CostingSheet; comparison: CostingSheetComparison }> {
    return this.http.post<{ sheet: CostingSheet; comparison: CostingSheetComparison }>(
      `${BASE}/costing-sheets/${id}/recost`,
      body,
    );
  }
}

/** Maps the catalog filter to the API's query parameters, dropping empty values. */
export function designParams(f: DesignFilter): HttpParams {
  let p = new HttpParams();
  const set = (k: string, v: string | number | null | undefined) => {
    if (v !== null && v !== undefined && `${v}`.trim() !== '') p = p.set(k, `${v}`.trim());
  };
  set('q', f.q?.trim());
  set('limit', f.limit);
  set('offset', f.offset);
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

/** Go can serialise an empty list as null; screens expect an array, so normalise it here. */
export function orEmpty<T>() {
  return map((v: T[] | null | undefined): T[] => v ?? []);
}
