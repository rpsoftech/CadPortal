import { Injectable, computed, signal } from '@angular/core';

/** A design picked in the catalog. */
export interface SelectedDesign {
  id: number;
  code: string;
  name: string;
}

/**
 * The designs ticked in the catalog. It lives for the whole session (not per page), so a selection
 * survives paging, filtering and a visit to another screen. Anything that acts on a selection (costing,
 * and later other bulk actions) reads this store and adds its button to the selection bar.
 */
@Injectable({ providedIn: 'root' })
export class CatalogSelection {
  private readonly picked = signal<ReadonlyMap<number, SelectedDesign>>(new Map());

  /** Selected designs, in the order they were picked. */
  readonly items = computed(() => [...this.picked().values()]);
  readonly ids = computed(() => [...this.picked().keys()]);
  readonly count = computed(() => this.picked().size);
  readonly isEmpty = computed(() => this.picked().size === 0);

  has(id: number): boolean {
    return this.picked().has(id);
  }

  toggle(d: SelectedDesign): void {
    this.update((m) => {
      if (!m.delete(d.id)) m.set(d.id, { id: d.id, code: d.code, name: d.name });
    });
  }

  /** Ticks all of `designs` (already ticked ones keep their place). */
  addMany(designs: readonly SelectedDesign[]): void {
    this.update((m) => {
      for (const d of designs) if (!m.has(d.id)) m.set(d.id, { id: d.id, code: d.code, name: d.name });
    });
  }

  removeMany(ids: readonly number[]): void {
    this.update((m) => ids.forEach((id) => m.delete(id)));
  }

  clear(): void {
    this.picked.set(new Map());
  }

  private update(f: (m: Map<number, SelectedDesign>) => void): void {
    const next = new Map(this.picked());
    f(next);
    this.picked.set(next);
  }
}
