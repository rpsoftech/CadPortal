import { Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { catchError, map, mergeMap, of, toArray } from 'rxjs';
import { ApiService } from '../../core/api.service';
import { CatalogSelection } from '../../core/catalog-selection';
import { toApiError } from '../../core/errors';
import { fixed } from '../../core/format';
import { OrderProblem } from '../../core/models';

/** One variation on the way into an order. */
export interface NewLine {
  variationId: number;
  designCode: string;
  designName: string;
  variationCode: string;
  /** Net weights by metal code, as the catalog has them (for a quick look; the order stores its own). */
  net: Record<string, string>;
  quantity: number;
}

/** Something that is not on the order, and why. */
export interface LeftOut {
  label: string;
  reason: string;
}

export const MAX_QUANTITY = 100000;
export const MAX_LINES = 500;

/** A whole number from 1 to MAX_QUANTITY, or null. */
export function parseQuantity(raw: string): number | null {
  const s = raw.trim();
  if (!/^\d+$/.test(s)) return null;
  const n = Number(s);
  return n >= 1 && n <= MAX_QUANTITY ? n : null;
}

/**
 * The review step of a new order. The designs ticked in the catalog are expanded into their variations
 * (one line each, quantity 1), the person checks quantities, removes what they do not want, and the order
 * is stored as a snapshot. Lines the server refuses (for example missing stone data) are named in its
 * words and can be left out in one tap.
 */
@Component({
  selector: 'app-order-new',
  imports: [
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    MatSelectModule,
  ],
  template: `
    <div class="page">
      <h1>New order</h1>
      @if (loading()) {
        <mat-progress-bar mode="indeterminate" aria-label="Loading the selected designs" />
      }
      @if (!loading() && lines().length === 0 && leftOut().length === 0) {
        <p class="muted">
          Nothing is selected. Tick designs in the <a routerLink="/">catalog</a>, then choose “Create order”.
        </p>
      }

      @if (lines().length > 0 || leftOut().length > 0) {
        <form [formGroup]="form" (ngSubmit)="save(false)" aria-label="New order" class="col">
          <section aria-labelledby="h-order" class="col">
            <h2 id="h-order">Order</h2>
            <div class="row">
              <mat-form-field appearance="outline" class="field">
                <mat-label>Metal</mat-label>
                <mat-select formControlName="metal" (selectionChange)="clearProblems()">
                  @for (m of metals(); track m) {
                    <mat-option [value]="m">{{ m }}</mat-option>
                  }
                </mat-select>
                <mat-error>Choose a metal</mat-error>
              </mat-form-field>
              <mat-form-field appearance="outline" class="field">
                <mat-label>Stone code</mat-label>
                <mat-select formControlName="stone" (selectionChange)="clearProblems()">
                  @for (s of stones(); track s) {
                    <mat-option [value]="s">{{ s }}</mat-option>
                  }
                </mat-select>
                <mat-error>Choose a stone code</mat-error>
              </mat-form-field>
              <mat-form-field appearance="outline" class="field">
                <mat-label>Due date</mat-label>
                <input matInput type="date" formControlName="dueDate" />
              </mat-form-field>
            </div>
            <mat-form-field appearance="outline">
              <mat-label>Title / buyer</mat-label>
              <input matInput formControlName="name" maxlength="200" autocomplete="off" />
              <mat-hint>Free text, for example the buyer and the occasion</mat-hint>
            </mat-form-field>
            <mat-form-field appearance="outline">
              <mat-label>Notes</mat-label>
              <textarea matInput formControlName="notes" rows="3" maxlength="2000"></textarea>
            </mat-form-field>
          </section>

          <section aria-labelledby="h-lines" class="col">
            <h2 id="h-lines">Lines</h2>
            <p class="muted" role="status">
              {{ lines().length }} line{{ lines().length === 1 ? '' : 's' }}, {{ pieces() }} piece{{
                pieces() === 1 ? '' : 's'
              }}.
              Each line is one variation; the quantity is the number of pieces.
            </p>
            @if (lines().length > MAX_LINES) {
              <div class="error-box" role="alert">
                An order holds at most {{ MAX_LINES }} lines; remove {{ lines().length - MAX_LINES }}.
              </div>
            }
            @if (lines().length > 0) {
              <table class="plain stack" aria-label="Order lines">
                <thead>
                  <tr>
                    <th scope="col">Design</th>
                    <th scope="col">Variation</th>
                    <th scope="col" class="num">Net g ({{ form.controls.metal.value || 'metal' }})</th>
                    <th scope="col" class="qty">Quantity</th>
                    <th scope="col"><span class="sr-only">Remove</span></th>
                  </tr>
                </thead>
                <tbody>
                  @for (l of lines(); track l.variationId) {
                    <tr [class.bad]="problemFor(l.variationId)">
                      <td data-label="Design">
                        <span class="mono">{{ l.designCode }}</span>
                        @if (l.designName) {
                          <span class="muted"> {{ l.designName }}</span>
                        }
                      </td>
                      <td data-label="Variation" class="mono">{{ l.variationCode }}</td>
                      <td data-label="Net g" class="num">{{ netOf(l) }}</td>
                      <td data-label="Quantity" class="qty">
                        <input
                          class="qty-input"
                          type="number"
                          inputmode="numeric"
                          min="1"
                          [max]="MAX_QUANTITY"
                          step="1"
                          [value]="l.quantity"
                          (input)="setQuantity(l, $any($event.target).value)"
                          [attr.aria-label]="'Quantity of ' + l.variationCode"
                          [attr.aria-invalid]="quantityBad(l) ? 'true' : null"
                        />
                        @if (quantityBad(l)) {
                          <span class="err" role="alert">1 to {{ MAX_QUANTITY }}</span>
                        }
                        @if (problemFor(l.variationId); as p) {
                          <span class="err" role="alert">{{ p.message }}</span>
                        }
                      </td>
                      <td class="row-actions">
                        <button
                          matButton
                          type="button"
                          (click)="remove(l)"
                          [attr.aria-label]="'Remove ' + l.variationCode + ' from the order'"
                        >
                          Remove
                        </button>
                      </td>
                    </tr>
                  }
                </tbody>
              </table>
            } @else {
              <p class="muted">All lines were removed. Go back to the catalog to select more designs.</p>
            }
          </section>

          @if (leftOut().length > 0) {
            <section aria-labelledby="h-left" class="col">
              <h2 id="h-left">Left out</h2>
              <ul class="left">
                @for (x of leftOut(); track x.label) {
                  <li>
                    <span class="mono">{{ x.label }}</span>: {{ x.reason }}
                  </li>
                }
              </ul>
            </section>
          }

          @if (error(); as e) {
            <div class="error-box" role="alert">
              {{ e }}
              @if (problems().length > 0) {
                <p>
                  <button matButton type="button" (click)="leaveOutProblems()">
                    Leave out the {{ problems().length }} line{{ problems().length === 1 ? '' : 's' }} that cannot be ordered
                  </button>
                </p>
              }
            </div>
          }
          @if (busy()) {
            <mat-progress-bar mode="indeterminate" aria-label="Saving the order" />
          }

          <div class="row actions">
            <a matButton routerLink="/">Back to the catalog</a>
            <span class="spacer"></span>
            <button matButton type="submit" [disabled]="!canSave() || busy()">Save as draft</button>
            <button matButton="filled" type="button" [disabled]="!canSave() || busy()" (click)="save(true)">
              Create and place
            </button>
          </div>
        </form>
      }
    </div>
  `,
  styles: `
    h1 {
      margin: 0 0 8px;
    }
    h2 {
      margin: 8px 0 0;
      font-size: 1.15rem;
    }
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .field {
      width: 190px;
    }
    .qty {
      width: 150px;
    }
    .qty-input {
      width: 7em;
      min-height: 40px;
      padding: 4px 8px;
      font: inherit;
      color: inherit;
      background: transparent;
      border: 1px solid var(--mat-sys-outline);
      border-radius: 6px;
      text-align: right;
    }
    .err {
      display: block;
      color: var(--mat-sys-error);
      font-size: 0.85rem;
    }
    tr.bad {
      outline: 2px solid var(--mat-sys-error);
    }
    .left {
      margin: 0;
      padding-left: 20px;
    }
    .actions {
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
    }
    .spacer {
      flex: 1;
    }
    @media (max-width: 720px) {
      .field {
        width: 100%;
      }
      .qty {
        width: auto;
      }
    }
  `,
})
export class OrderNewComponent {
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private readonly selection = inject(CatalogSelection);

  protected readonly MAX_QUANTITY = MAX_QUANTITY;
  protected readonly MAX_LINES = MAX_LINES;

  protected readonly metals = toSignal(
    this.api.metals().pipe(
      map((ms) => ms.filter((m) => m.active).map((m) => m.code)),
      catchError(() => of([] as string[])),
    ),
    { initialValue: [] as string[] },
  );
  protected readonly stones = toSignal(
    this.api.stoneCodes().pipe(
      map((ss) => ss.filter((s) => s.active).map((s) => s.code)),
      catchError(() => of([] as string[])),
    ),
    { initialValue: [] as string[] },
  );

  protected readonly form = new FormGroup({
    metal: new FormControl(this.route.snapshot.queryParamMap.get('metal') ?? '', {
      nonNullable: true,
      validators: Validators.required,
    }),
    stone: new FormControl(this.route.snapshot.queryParamMap.get('stone') ?? '', {
      nonNullable: true,
      validators: Validators.required,
    }),
    name: new FormControl('', { nonNullable: true, validators: Validators.maxLength(200) }),
    dueDate: new FormControl('', { nonNullable: true }),
    notes: new FormControl('', { nonNullable: true, validators: Validators.maxLength(2000) }),
  });

  protected readonly loading = signal(true);
  protected readonly lines = signal<NewLine[]>([]);
  protected readonly leftOut = signal<LeftOut[]>([]);
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly problems = signal<OrderProblem[]>([]);
  /** Raw text of quantity boxes that do not hold a valid number, by variation id. */
  private readonly badQty = signal<ReadonlySet<number>>(new Set());

  protected readonly pieces = computed(() => this.lines().reduce((n, l) => n + l.quantity, 0));
  protected readonly canSave = computed(
    () =>
      this.lines().length > 0 &&
      this.lines().length <= MAX_LINES &&
      this.badQty().size === 0 &&
      this.formValid(),
  );
  private readonly formStatus = signal(false);

  constructor() {
    this.formStatus.set(this.form.valid);
    this.form.statusChanges.subscribe(() => this.formStatus.set(this.form.valid));
    this.load();
    // Only one active metal / stone code: nothing to choose.
    this.api.metals().subscribe((ms) => this.pickOnly(this.form.controls.metal, ms.filter((m) => m.active).map((m) => m.code)));
    this.api.stoneCodes().subscribe((ss) => this.pickOnly(this.form.controls.stone, ss.filter((s) => s.active).map((s) => s.code)));
  }

  private formValid(): boolean {
    this.formStatus();
    return this.form.valid;
  }

  private pickOnly(ctl: FormControl<string>, codes: string[]): void {
    if (!ctl.value && codes.length === 1) ctl.setValue(codes[0]);
  }

  /** Expands every selected design into its variations; a design that cannot be read is listed as left out. */
  private load(): void {
    const designs = this.selection.items();
    if (designs.length === 0) {
      this.loading.set(false);
      return;
    }
    of(...designs)
      .pipe(
        mergeMap(
          (d) =>
            this.api.getDesign(d.code).pipe(
              map((detail) => ({ d, detail, err: '' })),
              catchError(async (e) => ({ d, detail: null, err: (await toApiError(e)).message })),
            ),
          4,
        ),
        toArray(),
      )
      .subscribe((results) => {
        const order = new Map(designs.map((d, i) => [d.id, i]));
        results.sort((a, b) => (order.get(a.d.id) ?? 0) - (order.get(b.d.id) ?? 0));
        const lines: NewLine[] = [];
        const left: LeftOut[] = [];
        for (const r of results) {
          if (!r.detail) {
            left.push({ label: r.d.code, reason: r.err || 'could not be read' });
          } else if (r.detail.variations.length === 0) {
            left.push({ label: r.d.code, reason: 'it has no variations yet' });
          } else {
            for (const v of r.detail.variations) {
              lines.push({
                variationId: v.id,
                designCode: r.detail.code,
                designName: r.detail.name,
                variationCode: v.code,
                net: Object.fromEntries(v.weights.map((w) => [w.metal_code, w.net_weight_g])),
                quantity: 1,
              });
            }
          }
        }
        this.lines.set(lines);
        this.leftOut.set(left);
        this.loading.set(false);
      });
  }

  protected netOf(l: NewLine): string {
    const m = this.form.controls.metal.value;
    return m && l.net[m] !== undefined ? fixed(l.net[m], 3) : '–';
  }

  protected setQuantity(l: NewLine, raw: string): void {
    const n = parseQuantity(raw);
    const bad = new Set(this.badQty());
    if (n === null) bad.add(l.variationId);
    else {
      bad.delete(l.variationId);
      this.lines.update((ls) => ls.map((x) => (x.variationId === l.variationId ? { ...x, quantity: n } : x)));
    }
    this.badQty.set(bad);
  }

  protected quantityBad(l: NewLine): boolean {
    return this.badQty().has(l.variationId);
  }

  protected remove(l: NewLine): void {
    this.lines.update((ls) => ls.filter((x) => x.variationId !== l.variationId));
    const bad = new Set(this.badQty());
    bad.delete(l.variationId);
    this.badQty.set(bad);
    this.problems.update((ps) => ps.filter((p) => p.variation_id !== l.variationId));
    if (this.problems().length === 0) this.error.set('');
  }

  protected problemFor(variationId: number): OrderProblem | undefined {
    return this.problems().find((p) => p.variation_id === variationId);
  }

  /** The server's verdict belongs to one metal and stone code; once either changes it is stale. */
  protected clearProblems(): void {
    this.problems.set([]);
    this.error.set('');
  }

  protected leaveOutProblems(): void {
    const bad = new Map(this.problems().map((p) => [p.variation_id, p.message]));
    const left = this.lines()
      .filter((l) => bad.has(l.variationId))
      .map((l) => ({ label: l.variationCode, reason: bad.get(l.variationId)! }));
    this.leftOut.update((x) => [...x, ...left]);
    this.lines.update((ls) => ls.filter((l) => !bad.has(l.variationId)));
    this.problems.set([]);
    this.error.set('');
  }

  protected save(place: boolean): void {
    if (!this.canSave() || this.busy()) return;
    const f = this.form.getRawValue();
    this.busy.set(true);
    this.error.set('');
    this.problems.set([]);
    const criteria = (history.state as { criteria?: Record<string, unknown> } | null)?.criteria;
    this.api
      .createOrder({
        metal_code: f.metal,
        stone_code: f.stone,
        name: f.name.trim() || undefined,
        notes: f.notes.trim() || undefined,
        due_date: f.dueDate || undefined,
        items: this.lines().map((l) => ({ variation_id: l.variationId, quantity: l.quantity })),
        criteria: criteria && Object.keys(criteria).length ? criteria : undefined,
        place,
      })
      .subscribe({
        next: (o) => {
          this.selection.clear();
          void this.router.navigate(['/orders', o.id]);
        },
        error: async (e) => {
          const err = await toApiError(e);
          this.error.set(err.message);
          this.problems.set(err.orderProblems);
          this.busy.set(false);
        },
      });
  }
}
