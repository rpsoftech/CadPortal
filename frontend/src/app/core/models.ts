// Wire types for the CadPortal API (v1). Weights, carats and money travel as
// decimal strings (never numbers) so no precision is lost in JavaScript.

export type Dec = string;

export type UserRole = 'admin' | 'designer' | 'production';

export interface User {
  id: number;
  email: string;
  name: string;
  role: UserRole;
  active: boolean;
  last_login_at: string | null;
  created_at: string;
}

export interface LoginResponse {
  token: string;
  expires_at: string;
  user: User;
}

export interface Category {
  id: number;
  name: string;
  code_prefix: string | null;
  /** The last design number handed out in this category. */
  next_seq: number;
}

export type TagGroup = 'Audience' | 'Sub-Style' | 'Motif' | 'Attribute';
export const TAG_GROUPS: TagGroup[] = ['Audience', 'Sub-Style', 'Motif', 'Attribute'];

export interface Tag {
  id: number;
  name: string;
  tag_group: TagGroup;
  active: boolean;
}

export interface Metal {
  id: number;
  code: string;
  specific_gravity: Dec;
  wastage_pct: Dec | null;
  is_default_input: boolean;
  active: boolean;
}

export interface StoneCode {
  id: number;
  code: string;
  is_default_baseline: boolean;
  active: boolean;
}

export type RateBasis = 'pc' | 'ct';

export interface StoneMatrixRow {
  size_mm: Dec;
  stone_code_id: number;
  stone_code: string;
  sieve: string | null;
  carats_per_stone: Dec;
  rate_per_ct: Dec | null;
  rate_per_pc: Dec | null;
  /** Which price costing uses for this row: per piece (the default) or per carat. */
  rate_basis: RateBasis;
}

/** One row sent to PUT /masters/stone-matrix. */
export interface MatrixRowInput {
  size_mm: string;
  stone_code: string;
  sieve: string;
  carats_per_stone: string;
  rate_per_pc: string | null;
  rate_per_ct: string | null;
  /** Empty lets the server decide: per piece, unless only a per-carat price is given. */
  rate_basis: RateBasis | '';
}

export interface MatrixGap {
  size_mm: Dec;
  stone_code: string;
  variation_count: number;
}

export interface RecalcStatus {
  pending: number;
  running: number;
  failed: number;
  last_error: string;
}

export interface MetalInput {
  specific_gravity: string;
  wastage_pct: string | null;
  is_default_input: boolean;
  active: boolean;
}

export interface AuditRecord {
  id: number;
  at: string;
  user_id: number | null;
  user_email: string;
  action: string;
  entity_type: string;
  entity_id: string;
  status: number;
  detail: string;
}

export interface AuditFilter {
  /** Matches the start of the action, e.g. "metal." */
  action?: string;
  userId?: number | null;
  /** YYYY-MM-DD, inclusive. */
  from?: string;
  to?: string;
  limit?: number;
  offset?: number;
}

export interface DesignListItem {
  id: number;
  code: string;
  name: string;
  category_id: number;
  category_name: string;
  tag_names: string[];
  variation_count: number;
  /** A signed link to the cover photo's thumbnail, or empty when the design has no photo. */
  cover_url: string;
  net_min: Dec | null;
  net_max: Dec | null;
  gross_min: Dec | null;
  gross_max: Dec | null;
}

export interface DesignFilter {
  /** Matches the design code or name (substring, case-insensitive). */
  q?: string | null;
  limit?: number;
  offset?: number;
  categoryId?: number | null;
  tagIds?: number[];
  metalCode?: string | null;
  minNetWt?: string | null;
  maxNetWt?: string | null;
  minGrossWt?: string | null;
  maxGrossWt?: string | null;
  maxStoneCts?: string | null;
}

export interface VariationWeight {
  variation_id: number;
  metal_id: number;
  metal_code: string;
  net_weight_g: Dec;
  gross_weight_g: Dec | null;
}

export interface Variation {
  id: number;
  design_id: number;
  seq: number;
  code: string;
  base_volume_mm3: Dec | null;
  base_ref_weight_g: Dec | null;
  base_ref_metal_id: number | null;
  file_3dm_sha256: string | null;
  file_3dm_name: string | null;
  file_3dm_size_bytes: number | null;
  file_stl_sha256: string | null;
  file_stl_name: string | null;
  file_stl_size_bytes: number | null;
  baseline_stone_carats: Dec;
  baseline_gross_weight_g: Dec | null;
  weights: VariationWeight[];
}

export interface DesignPage {
  items: DesignListItem[];
  /** All matches before paging. */
  total: number;
}

export interface DesignDetail {
  id: number;
  code: string;
  name: string;
  category_id: number;
  tags: Tag[];
  variations: Variation[];
  media: MediaItem[];
}

export interface BaseInput {
  volume_mm3?: string;
  ref_weight_g?: string;
  ref_metal_code?: string;
}

export interface Seat {
  id: number;
  variation_id: number;
  position: number;
  size_mm: Dec;
  stone_count: number;
  locked_stone_code_id: number | null;
  locked_code: string | null;
}

export interface SeatInput {
  size_mm: string;
  stone_count: number;
  locked_stone_code: string;
}

export interface BomLine {
  size_mm: Dec;
  stone_count: number;
  stone_code: string;
  locked: boolean;
  carats_per_stone: Dec;
  line_carats: Dec;
  /** line_carats x 0.200, 4 decimal places */
  line_weight_g: Dec;
}

export interface MetalTotals {
  metal_code: string;
  net_weight_g: Dec;
  gross_weight_g: Dec;
}

export interface BomResult {
  variation_code: string;
  stone_code: string;
  lines: BomLine[];
  total_pcs: number;
  total_carats: Dec;
  stone_weight_g: Dec;
  metals: MetalTotals[];
}

export interface UploadResult {
  variation_code: string;
  kind: '3dm' | 'stl';
  filename: string;
  sha256: string;
  size_bytes: number;
}

export interface DownloadLink {
  url: string;
  filename: string;
  expires_in_seconds: number;
}

export interface CostingItem {
  variation_id: number;
  metal_code: string;
  stone_code: string;
}

/** Body of POST /exports/job-bags-pdf. A per-item metal or stone code overrides the shared one. */
export interface JobBagsRequest {
  items?: { variation_id: number; metal_code?: string; stone_code?: string }[];
  design_ids?: number[];
  metal_code?: string;
  stone_code?: string;
  skip_incomplete?: boolean;
}

/** Error body shape returned by the API. Go structs without json tags use PascalCase keys. */
export interface ApiErrorBody {
  error?: string;
  missing?: { SizeMM: string; Code: string; Basis?: string }[];
  stone_rates_missing?: { SizeMM: string; Code: string; Basis?: string }[];
  metals_missing_wastage?: string[];
}

/** One line of a design import file, as sent to POST /designs/import. */
export interface DesignImportRow {
  line: number;
  code: string;
  name: string;
  category: string;
  tags: string;
  volume_mm3: string;
  ref_weight_g: string;
  ref_metal: string;
}

export interface DesignImportResult {
  dry_run: boolean;
  designs: number;
  variations: number;
  errors: { line: number; message: string }[];
  /** The codes of the designs an import created, in file order. */
  codes: string[];
}

/** One thing an admin still has to enter, from GET /masters/setup-status. */
export interface SetupItem {
  key: string;
  severity: 'required' | 'recommended';
  title: string;
  detail: string;
  /** The Master data tab where it is fixed; empty for something fixed outside the app (backups). */
  tab: 'alloys' | 'matrix' | 'categories' | '';
}

export interface SetupStatus {
  complete: boolean;
  items: SetupItem[];
}

/** A photo or video of a design (variation_id null) or of one of its variations. */
export interface MediaItem {
  id: number;
  design_id: number;
  variation_id: number | null;
  kind: 'image' | 'video';
  content_type: string;
  filename: string;
  size_bytes: number;
  width: number | null;
  height: number | null;
  position: number;
  /** Only a photo of the whole design can be the cover of its catalog card. */
  is_cover: boolean;
  /** Signed links that expire; the page asks again when it reloads. */
  url: string;
  thumb_url?: string;
}

/** A design that was deleted and can be brought back. */
export interface DeletedDesign {
  id: number;
  code: string;
  name: string;
  category_name: string;
  deleted_at: string;
  variation_count: number;
}

/** A removed or replaced CAD file, photo or video, kept until purge_after. */
export interface TrashedFile {
  id: number;
  kind: 'cad' | 'media';
  label: string;
  reason: 'removed' | 'replaced' | 'variation_deleted';
  deleted_at: string;
  purge_after: string;
  /** False when its variation was deleted: the stored copy is kept, but the app cannot put it back. */
  restorable: boolean;
  has_thumbnail: boolean;
}

/** A deleted variation: kept with its seats, files and photos until purge_after. */
export interface DeletedVariation {
  id: number;
  code: string;
  design_code: string;
  design_name: string;
  deleted_at: string;
  /** Null when no retention period is set (nothing is purged automatically). */
  purge_after: string | null;
  has_files: boolean;
  /** True while its design is deleted: restore the design first. */
  design_deleted: boolean;
}

export interface Trash {
  designs: DeletedDesign[];
  variations: DeletedVariation[];
  files: TrashedFile[];
}

export interface DuplicateDesignBody {
  name?: string;
  variation_ids?: number[];
  copy_tags: boolean;
}

/** One run of the backup service. */
export interface BackupRun {
  id: number;
  started_at: string;
  finished_at: string | null;
  /** null while it runs (or if it was killed before finishing). */
  ok: boolean | null;
  db_bytes: number;
  files_copied: number;
  files_bytes: number;
  files_total: number;
  files_skipped: boolean;
  error: string;
}

/** A database dump stored in the backup bucket. */
export interface BackupDump {
  key: string;
  name: string;
  taken_at: string;
  bytes: number;
  encrypted: boolean;
}

export interface Backups {
  runs: BackupRun[];
  dumps: BackupDump[];
  /** Why the dump list is empty when the portal cannot see the backup bucket. */
  dumps_note: string;
  can_download: boolean;
  /** A "Back up now" is waiting for the backup service. */
  pending: boolean;
  /** A backup is in progress. */
  running: boolean;
}

/** What the sign-in screens need to know before anyone is signed in. */
export interface AuthConfig {
  /** "Forgot password" can send an email (a mail server is configured). */
  password_reset_email: boolean;
}

/** A one-hour reset link an admin hands to a user. `path` is relative to the portal's address. */
export interface ResetLink {
  path: string;
  expires_at: string;
}
