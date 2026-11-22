/** The size above which "leave out large stones" starts as: the largest stone the stone matrix has rows for. */
export const IGNORE_OVER_DEFAULT_MM = '2.00';

/** The part of a costing request that leaves large stones out: nothing at all when the choice is off. */
export function ignoreOverPart(on: boolean, mm: string | null): { ignore_stones_over_mm?: string } {
  const v = (mm ?? '').trim();
  return on && v ? { ignore_stones_over_mm: v } : {};
}
