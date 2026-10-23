import { MatrixRowInput, RateBasis, StoneMatrixRow } from './models';

/** How each pricing basis reads in the UI: per piece (the default), per carat, per gram of stone weight. */
export const BASIS_LABELS: Record<RateBasis, { long: string; short: string }> = {
  pc: { long: 'per piece', short: 'per pc' },
  ct: { long: 'per carat', short: 'per ct' },
  g: { long: 'per gram', short: 'per g' },
};

type PriceFields = Pick<StoneMatrixRow, 'rate_per_pc' | 'rate_per_ct' | 'rate_per_g'>;

/** The price a row holds for one basis, or null when none is set. */
export function priceFor(
  row: Partial<PriceFields> | null | undefined,
  basis: RateBasis,
): string | null {
  if (!row) return null;
  const p = basis === 'ct' ? row.rate_per_ct : basis === 'g' ? row.rate_per_g : row.rate_per_pc;
  return p ?? null;
}

/** The prices a row keeps on bases other than the one it is priced by (kept, not used for costing). */
export function otherPrices(
  row: Partial<PriceFields> | null | undefined,
  basis: RateBasis,
): { basis: RateBasis; price: string }[] {
  return (['pc', 'ct', 'g'] as RateBasis[])
    .filter((b) => b !== basis)
    .map((b) => ({ basis: b, price: priceFor(row, b) }))
    .filter((x): x is { basis: RateBasis; price: string } => !!x.price);
}

/**
 * The prices to send for a row being saved: the chosen basis takes the typed price and the other
 * bases keep what the row already held.
 */
export function pricesForSave(
  row: Partial<PriceFields> | null | undefined,
  basis: RateBasis,
  typed: string | null,
): Pick<MatrixRowInput, 'rate_per_pc' | 'rate_per_ct' | 'rate_per_g'> {
  return {
    rate_per_pc: basis === 'pc' ? typed : (row?.rate_per_pc ?? null),
    rate_per_ct: basis === 'ct' ? typed : (row?.rate_per_ct ?? null),
    rate_per_g: basis === 'g' ? typed : (row?.rate_per_g ?? null),
  };
}
