import { describe, expect, it } from 'vitest';
import { ORDER_NEXT, ORDER_STATUSES } from '../../core/models';

describe('order status flow', () => {
  it('moves draft -> placed -> in production -> done, and cancels only before done', () => {
    expect(ORDER_NEXT.draft).toEqual(['placed', 'cancelled']);
    expect(ORDER_NEXT.placed).toEqual(['in_production', 'cancelled']);
    expect(ORDER_NEXT.in_production).toEqual(['done', 'cancelled']);
  });

  it('has no way out of a final state', () => {
    expect(ORDER_NEXT.done).toEqual([]);
    expect(ORDER_NEXT.cancelled).toEqual([]);
  });

  it('knows every status', () => {
    expect(Object.keys(ORDER_NEXT).sort()).toEqual([...ORDER_STATUSES].sort());
  });
});
