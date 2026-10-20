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

export interface StoneMatrixRow {
  size_mm: Dec;
  stone_code_id: number;
  stone_code: string;
  sieve: string | null;
  carats_per_stone: Dec;
  rate_per_ct: Dec | null;
}

/** One row sent to PUT /masters/stone-matrix. */
export interface MatrixRowInput {
  size_mm: string;
  stone_code: string;
  sieve: string;
  carats_per_stone: string;
  rate_per_ct: string | null;
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

export interface DesignListItem {
  id: number;
  code: string;
  name: string;
  category_id: number;
  category_name: string;
  tag_names: string[];
  variation_count: number;
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

/** Error body shape returned by the API. Go structs without json tags use PascalCase keys. */
export interface ApiErrorBody {
  error?: string;
  missing?: { SizeMM: string; Code: string }[];
  stone_rates_missing?: { SizeMM: string; Code: string }[];
  metals_missing_wastage?: string[];
}
