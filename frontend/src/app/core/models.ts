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

export type RateBasis = 'pc' | 'ct' | 'g';

export interface StoneMatrixRow {
  size_mm: Dec;
  stone_code_id: number;
  stone_code: string;
  sieve: string | null;
  carats_per_stone: Dec;
  rate_per_ct: Dec | null;
  rate_per_pc: Dec | null;
  /** Price per gram of stone weight (carats x 0.200). */
  rate_per_g: Dec | null;
  /** Which price costing uses for this row: per piece (the default), per carat or per gram. */
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
  rate_per_g: string | null;
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
  /** The stone code the stone figures below were computed for. */
  stone_code?: string;
  /** The variation the figures below belong to (with a metal selected). */
  shown_variation_id?: number | null;
  shown_variation_code?: string;
  net_g?: Dec | null;
  stone_carats?: Dec | null;
  stone_g?: Dec | null;
  gross_g?: Dec | null;
  /** Stone weight as a percentage of the gross weight; null when there is no data. */
  stone_pct?: Dec | null;
  /** "ok", or "no_data" when a stone matrix row is missing for the code (never shown as 0). */
  stone_status?: '' | 'ok' | 'no_data';
  stone_note?: string;
}

/** The catalog sort orders the API understands (weights and stone % need a metal). */
export type DesignSort =
  | 'code'
  | 'net_asc'
  | 'net_desc'
  | 'gross_asc'
  | 'gross_desc'
  | 'stone_pct_asc'
  | 'stone_pct_desc';

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
  /** The stone code the stone figures are computed for; empty is the baseline code. */
  stoneCode?: string | null;
  /** Stone weight as a percentage of gross weight (needs metalCode). */
  minStonePct?: string | null;
  maxStonePct?: string | null;
  sort?: DesignSort | null;
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

/** What to cost: whole designs and/or single variations, in one metal and one stone code. */
export interface CostSheetRequest {
  design_ids?: number[];
  variation_ids?: number[];
  metal_code: string;
  stone_code: string;
  gold_rate_per_g: string;
  setting_rate_per_pc: string;
  /** Cost the complete variations and list the others, instead of costing nothing. */
  skip_incomplete: boolean;
  /** Pieces per variation id (an order's quantities): the rows are then for that many pieces. */
  quantities?: Record<number, number>;
}

/** One costed variation. Money and weights are decimal strings straight from the API. */
export interface CostSheetRow {
  design_id: number;
  design_code: string;
  design_name: string;
  variation_id: number;
  variation_code: string;
  metal_code: string;
  stone_code: string;
  /** Pieces the row stands for (an order's quantity); amounts and weights are for the whole line. */
  quantity?: number;
  net_g: string;
  gross_g: string;
  wastage_pct: string;
  gold_g: string;
  gold_rate_per_g: string;
  gold_cost: string;
  stone_ct: string;
  stone_g: string;
  stone_cost: string;
  setting_pcs: number;
  setting_rate_per_pc: string;
  setting_cost: string;
  total: string;
}

export interface CostSheetTotals {
  designs: number;
  variations: number;
  net_g: string;
  gross_g: string;
  gold_g: string;
  gold_cost: string;
  stone_ct: string;
  stone_g: string;
  stone_cost: string;
  setting_pcs: number;
  setting_cost: string;
  total: string;
}

export interface CostVariationProblem {
  variation_id: number;
  variation_code: string;
  message: string;
  metals_missing_wastage?: string[];
  stone_rates_missing?: { SizeMM: string; Code: string; Basis?: string }[];
  matrix_missing?: { SizeMM: string; Code: string }[];
}

/** The problems of one design; a design with no variations has an empty list and a message. */
export interface CostDesignProblem {
  design_id: number;
  design_code: string;
  design_name: string;
  message?: string;
  variations: CostVariationProblem[];
}

export interface CostPreview {
  rows: CostSheetRow[];
  totals: CostSheetTotals;
  problems: CostDesignProblem[];
  complete: boolean;
}

export interface CostingSheetSummary {
  id: number;
  name: string;
  /** The order this sheet was made from, if any. */
  order_id?: number | null;
  created_at: string;
  created_by: number | null;
  created_by_name: string;
  recost_of: number | null;
  metal_code: string;
  stone_code: string;
  gold_rate_per_g: string;
  setting_rate_per_pc: string;
  design_count: number;
  variation_count: number;
  incomplete_count: number;
  total: string;
}

/** A saved sheet: everything in it is as it was when it was saved. */
export interface CostingSheet extends CostingSheetSummary {
  inputs: CostSheetRequest & { resolved_variation_ids: number[] };
  rows: CostSheetRow[];
  totals: CostSheetTotals;
  problems: CostDesignProblem[];
  rates: {
    gold_rate_per_g: string;
    setting_rate_per_pc: string;
    metals: { metal_code: string; specific_gravity: string; wastage_pct: string }[];
    stone_rates: {
      size_mm: string;
      stone_code: string;
      rate_basis: RateBasis;
      price: string;
      carats_per_stone: string;
    }[];
  };
}

export interface CostingSheetComparison {
  previous_id: number;
  previous_total: string;
  total: string;
  difference: string;
  rows: {
    variation_id: number;
    variation_code: string;
    design_code: string;
    previous_total: string | null;
    total: string | null;
    difference: string | null;
  }[];
}

/** Body of POST /exports/job-bags-pdf. A per-item metal or stone code overrides the shared one. */
export interface JobBagsRequest {
  items?: { variation_id: number; metal_code?: string; stone_code?: string }[];
  design_ids?: number[];
  metal_code?: string;
  stone_code?: string;
  skip_incomplete?: boolean;
}

/** Which CAD file type a ZIP download collects. */
export type CadZipKind = 'stl' | '3dm';

/** Body of POST /downloads/cad-zip/link: designs stand for all their live variations. */
export interface CadZipRequest {
  design_ids?: number[];
  variation_ids?: number[];
  kinds: CadZipKind[];
}

/**
 * What a ZIP of CAD files would hold, plus a short-lived signed link to fetch it. The link is opened as a
 * normal browser download (no bearer token needed), so the browser saves the stream straight to disk.
 */
export interface CadZipLink {
  url: string;
  expires_in_seconds: number;
  filename: string;
  variations: number;
  /** Files that will be in the ZIP. */
  files: number;
  /** Requested files that do not exist (the variation has none of that kind); listed in MANIFEST.csv. */
  missing: number;
  /** Total size of the original files. */
  bytes: number;
  kinds: CadZipKind[];
}

/**
 * What an order download holds and a signed link to fetch it (GET /orders/:id/download/link and
 * /orders/:id/package/link). The files are the ones the order recorded, not the variations as they are now.
 */
export interface OrderZipLink {
  url: string;
  expires_in_seconds: number;
  filename: string;
  order: string;
  lines: number;
  /** Files that will be in the ZIP. */
  files: number;
  /** Requested files the order recorded none of; listed in MANIFEST.csv. */
  missing: number;
  /** Files whose variation has a different file now, no file, or is gone; the recorded copy is shipped. */
  changed: number;
  /** Total size of the original files. */
  bytes: number;
  kinds: CadZipKind[];
  /** True for the package (CAD files plus job-bag PDF and summary CSV). */
  package: boolean;
}

/** Error body shape returned by the API. Go structs without json tags use PascalCase keys. */
export interface ApiErrorBody {
  error?: string;
  /** Costing of several designs: what is missing, grouped by design and variation. */
  problems?: (CostDesignProblem | OrderProblem)[];
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
  /** Minutes without activity before the app signs the user out; 0 = never. */
  idle_timeout_minutes: number;
}

/** A one-hour reset link an admin hands to a user. `path` is relative to the portal's address. */
export interface ResetLink {
  path: string;
  expires_at: string;
}

// ----- Orders (a selection of variations sent to production; every line is an immutable snapshot) -----

export type OrderStatus = 'draft' | 'placed' | 'in_production' | 'done' | 'cancelled';

export interface OrderItem {
  variation_id: number;
  quantity: number;
}

export interface OrderBomLine {
  size_mm: string;
  stone_count: number;
  stone_code: string;
  locked: boolean;
  carats_per_stone: string;
  line_carats: string;
  line_weight_g: string;
}

export interface OrderFileSnapshot {
  name: string;
  sha256: string;
  size_bytes: number;
}

export interface OrderLineSnapshot {
  snapshot_at: string;
  design_code: string;
  design_name: string;
  category_name: string;
  tags: string[];
  variation_code: string;
  metal_code: string;
  stone_code: string;
  specific_gravity: string;
  base_input: { kind: 'volume' | 'weight'; volume_mm3?: string; ref_weight_g?: string; ref_metal_code?: string };
  net_weight_g: string;
  stone_weight_g: string;
  stone_carats: string;
  gross_weight_g: string;
  /** Stone weight as a percentage of gross weight, 2 decimals. */
  stone_pct: string;
  total_pcs: number;
  bom: OrderBomLine[];
  file_3dm: OrderFileSnapshot | null;
  file_stl: OrderFileSnapshot | null;
  cover: { media_id: number; filename: string; content_type: string } | null;
}

export interface OrderLine {
  id: number;
  position: number;
  /** Null once the design / variation has been purged; the snapshot is still complete. */
  design_id: number | null;
  variation_id: number | null;
  quantity: number;
  snapshot: OrderLineSnapshot;
}

export interface OrderHistoryEntry {
  id: number;
  from_status: OrderStatus | null;
  to_status: OrderStatus;
  changed_by: number | null;
  changed_by_name: string;
  changed_at: string;
  note: string;
}

export interface OrderSummary {
  id: number;
  number: string;
  name: string;
  due_date: string | null;
  status: OrderStatus;
  metal_code: string;
  stone_code: string;
  cloned_from: number | null;
  created_by: number | null;
  created_by_name: string;
  created_at: string;
  updated_at: string;
  line_count: number;
  total_quantity: number;
}

export interface Order extends OrderSummary {
  notes: string;
  /** How the selection was found (optional, informational). */
  criteria: Record<string, unknown> | null;
  lines: OrderLine[];
  history: OrderHistoryEntry[];
}

export interface CreateOrderRequest {
  name?: string;
  notes?: string;
  due_date?: string;
  metal_code: string;
  stone_code: string;
  items: OrderItem[];
  criteria?: Record<string, unknown>;
  /** Create it as "placed" instead of a draft. */
  place?: boolean;
}

export interface OrderLineChange {
  field: string;
  old: string;
  new: string;
}

export interface OrderLineDiff {
  variation_id: number | null;
  design_code: string;
  variation_code: string;
  quantity: number;
  status: 'unchanged' | 'changed' | 'unavailable';
  reason?: string;
  changes: OrderLineChange[];
}

export interface OrderDiff {
  source_id: number;
  changed: number;
  unchanged: number;
  unavailable: number;
  lines: OrderLineDiff[];
}

/** The 422 body when some variations cannot be ordered (for example missing stone data). */
export interface OrderProblem {
  variation_id: number;
  variation_code?: string;
  message: string;
  missing?: { SizeMM: string; Code: string }[];
}

/** Body of POST /orders/:id/costing: metal and stone code come from the order. */
export interface CostOrderRequest {
  gold_rate_per_g: string;
  setting_rate_per_pc: string;
  skip_incomplete: boolean;
  name?: string;
}

/** The answer to costing an order: the saved sheet and how far the live designs have moved from the order. */
export interface CostOrderResult {
  sheet: CostingSheet;
  snapshot_differs: boolean;
  drift: OrderDiff;
}

/** How one order line compares with the catalog today (GET /orders/:id/live). */
export interface OrderLineLive {
  line_id: number;
  status: 'unchanged' | 'changed' | 'unavailable';
  reason?: string;
  changes: OrderLineChange[];
}

/** The next states an order may move to, and who sees which button. */
export const ORDER_NEXT: Record<OrderStatus, OrderStatus[]> = {
  draft: ['placed', 'cancelled'],
  placed: ['in_production', 'cancelled'],
  in_production: ['done', 'cancelled'],
  done: [],
  cancelled: [],
};

export const ORDER_STATUS_LABEL: Record<OrderStatus, string> = {
  draft: 'Draft',
  placed: 'Placed',
  in_production: 'In production',
  done: 'Done',
  cancelled: 'Cancelled',
};

export const ORDER_STATUSES: OrderStatus[] = ['draft', 'placed', 'in_production', 'done', 'cancelled'];
