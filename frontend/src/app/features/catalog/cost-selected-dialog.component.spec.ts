import { describe, expect, it } from 'vitest';
import { CostPreview } from '../../core/models';
import { costUsable } from './cost-selected-dialog.component';

const row = {} as CostPreview['rows'][number];
const problem = { design_id: 1, design_code: 'LR0001', design_name: '', variations: [] };
const preview = (rows: number, problems: number): CostPreview => ({
  rows: Array(rows).fill(row),
  totals: {} as CostPreview['totals'],
  problems: Array(problems).fill(problem),
  complete: rows > 0 && problems === 0,
});

describe('costUsable', () => {
  it('needs a preview with at least one costed row', () => {
    expect(costUsable(null, true)).toBe(false);
    expect(costUsable(preview(0, 0), true)).toBe(false);
    expect(costUsable(preview(0, 2), true)).toBe(false);
  });

  it('allows a complete costing', () => {
    expect(costUsable(preview(2, 0), false)).toBe(true);
  });

  it('refuses an incomplete costing unless the user chose to skip the missing ones', () => {
    expect(costUsable(preview(2, 1), false)).toBe(false);
    expect(costUsable(preview(2, 1), true)).toBe(true);
  });
});
