import { TestBed } from '@angular/core/testing';
import { CatalogSelection } from './catalog-selection';

const d = (id: number) => ({ id, code: `LR${id}`, name: `Design ${id}` });

describe('CatalogSelection', () => {
  let sel: CatalogSelection;
  beforeEach(() => {
    TestBed.configureTestingModule({});
    sel = TestBed.inject(CatalogSelection);
  });

  it('starts empty', () => {
    expect(sel.isEmpty()).toBe(true);
    expect(sel.count()).toBe(0);
    expect(sel.ids()).toEqual([]);
  });

  it('toggles a design on and off', () => {
    sel.toggle(d(1));
    expect(sel.has(1)).toBe(true);
    expect(sel.count()).toBe(1);
    sel.toggle(d(1));
    expect(sel.has(1)).toBe(false);
    expect(sel.isEmpty()).toBe(true);
  });

  it('adds a page at once without duplicating or reordering what was already ticked', () => {
    sel.toggle(d(2));
    sel.addMany([d(1), d(2), d(3)]);
    expect(sel.ids()).toEqual([2, 1, 3]);
    expect(sel.items().map((x) => x.code)).toEqual(['LR2', 'LR1', 'LR3']);
  });

  it('removes a page without touching designs from other pages', () => {
    sel.addMany([d(1), d(2), d(3), d(4)]);
    sel.removeMany([2, 3]);
    expect(sel.ids()).toEqual([1, 4]);
  });

  it('clears everything', () => {
    sel.addMany([d(1), d(2)]);
    sel.clear();
    expect(sel.isEmpty()).toBe(true);
  });
});
