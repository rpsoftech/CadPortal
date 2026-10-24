import { DatePipe } from '@angular/common';
import { Component, computed, effect, inject, input, signal, untracked } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { Router, RouterLink } from '@angular/router';
import { ApiService } from '../../core/api.service';
import { AuthService } from '../../core/auth.service';
import { toApiError } from '../../core/errors';
import { fixed } from '../../core/format';
import { saveBlob } from '../../shared/download';
import {
  CostingSheetSummary,
  ORDER_NEXT,
  ORDER_STATUS_LABEL,
  Order,
  OrderLine,
  OrderLineLive,
  OrderStatus,
} from '../../core/models';
import { OrderCloneData, OrderCloneDialogComponent } from './order-clone-dialog.component';
import { OrderCostData, OrderCostDialogComponent } from './order-cost-dialog.component';
import { parseQuantity } from './order-new.component';
import { OrderStatusDialogComponent, OrderStatusDialogData } from './order-status-dialog.component';
import { OrderStatusComponent } from './order-status.component';

/** The button for each move: its label, the dialog text and whether it is the destructive one. */
const STEP: Record<string, { label: string; title: string; message: string }> = {
  placed: {
    label: 'Place order',
    title: 'Place this order?',
    message: 'The lines can no longer be changed once the order is placed.',
  },
  in_production: {
    label: 'Start production',
    title: 'Start production?',
    message: 'The order moves to “In production”.',
  },
  done: { label: 'Mark done', title: 'Mark this order done?', message: 'A done order is final.' },
  cancelled: {
    label: 'Cancel order',
    title: 'Cancel this order?',
    message: 'A cancelled order stays in the list for the record, but it cannot be reopened.',
  },
};

/**
 * One order, read from its own snapshot: editing a design afterwards never changes what is shown here.
 * A line marked “changed since” or “no longer available” compares the snapshot with the catalog today.
 */
@Component({
  selector: 'app-order-detail',
  imports: [
    DatePipe,
    ReactiveFormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    OrderStatusComponent,
  ],
  templateUrl: './order-detail.component.html',
  styleUrl: './order-detail.component.scss',
})
export class OrderDetailComponent {
  /** The route's `:id` (component input binding). */
  readonly id = input.required<string>();

  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly router = inject(Router);
  protected readonly auth = inject(AuthService);

  protected readonly order = signal<Order | null>(null);
  protected readonly live = signal<ReadonlyMap<number, OrderLineLive>>(new Map());
  protected readonly loading = signal(true);
  protected readonly error = signal('');
  protected readonly busy = signal(false);
  /** Spoken to screen readers after a change (status moves, saves). */
  protected readonly announce = signal('');
  protected readonly actionError = signal('');

  /** The costing sheets made from this order (admin / designer only; the API refuses production). */
  protected readonly sheets = signal<CostingSheetSummary[]>([]);

  protected readonly statusLabel = ORDER_STATUS_LABEL;

  /** Writing is for admin and designer; a final order (done / cancelled) is read-only for everyone. */
  protected readonly canWrite = computed(() => this.auth.canEdit());
  protected readonly isOpen = computed(() => {
    const s = this.order()?.status;
    return s === 'draft' || s === 'placed' || s === 'in_production';
  });
  protected readonly nextSteps = computed<OrderStatus[]>(() => {
    const o = this.order();
    return o && this.canWrite() ? ORDER_NEXT[o.status] : [];
  });
  protected readonly isDraft = computed(() => this.order()?.status === 'draft');
  protected readonly step = STEP;

  protected readonly totalNet = computed(() => this.sum((l) => l.snapshot.net_weight_g, 3));
  protected readonly totalGross = computed(() => this.sum((l) => l.snapshot.gross_weight_g, 3));

  protected readonly header = new FormGroup({
    name: new FormControl('', { nonNullable: true, validators: Validators.maxLength(200) }),
    notes: new FormControl('', { nonNullable: true, validators: Validators.maxLength(2000) }),
    dueDate: new FormControl('', { nonNullable: true }),
  });

  /** Draft only: changed quantities and removed lines, by line id, until they are saved. */
  protected readonly qtyEdits = signal<ReadonlyMap<number, number>>(new Map());
  protected readonly badQty = signal<ReadonlySet<number>>(new Set());
  protected readonly removed = signal<ReadonlySet<number>>(new Set());
  protected readonly shownLines = computed(() =>
    (this.order()?.lines ?? []).filter((l) => !this.removed().has(l.id)),
  );
  protected readonly dirtyLines = computed(() => this.qtyEdits().size > 0 || this.removed().size > 0);

  constructor() {
    // the same component is reused when the address changes from one order to another
    effect(() => {
      this.id();
      untracked(() => this.load());
    });
  }

  private sum(pick: (l: OrderLine) => string, dp: number): string {
    // Decimal strings are added in thousandths as integers: no floating point on weights.
    let milli = 0n;
    for (const l of this.order()?.lines ?? []) {
      const v = pick(l);
      const m = /^(\d+)(?:\.(\d+))?$/.exec(v.trim());
      if (!m) return '–';
      const frac = (m[2] ?? '').padEnd(6, '0').slice(0, 6);
      milli += (BigInt(m[1]) * 1_000_000n + BigInt(frac)) * BigInt(l.quantity);
    }
    const whole = milli / 1_000_000n;
    const rest = (milli % 1_000_000n).toString().padStart(6, '0');
    return fixed(`${whole}.${rest}`.replace(/(\.\d{3})\d+$/, '$1'), dp);
  }

  protected load(): void {
    this.order.set(null);
    this.loading.set(true);
    this.error.set('');
    this.api.order(Number(this.id())).subscribe({
      next: (o) => {
        this.setOrder(o);
        this.loading.set(false);
        this.loadLive();
      },
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.loading.set(false);
      },
    });
  }

  private loadLive(): void {
    const id = Number(this.id());
    this.api.orderLive(id).subscribe({
      next: (ls) => this.live.set(new Map(ls.map((l) => [l.line_id, l]))),
      error: () => this.live.set(new Map()),
    });
  }

  private setOrder(o: Order): void {
    this.order.set(o);
    this.header.reset({ name: o.name, notes: o.notes, dueDate: o.due_date ?? '' });
    this.qtyEdits.set(new Map());
    this.badQty.set(new Set());
    this.removed.set(new Set());
    this.loadSheets();
  }

  protected lv(l: OrderLine): OrderLineLive | undefined {
    return this.live().get(l.id);
  }

  protected fx(v: string | null | undefined, dp: number): string {
    return fixed(v, dp);
  }

  protected qty(l: OrderLine): number {
    return this.qtyEdits().get(l.id) ?? l.quantity;
  }

  protected setQty(l: OrderLine, raw: string): void {
    const n = parseQuantity(raw);
    const bad = new Set(this.badQty());
    const edits = new Map(this.qtyEdits());
    if (n === null) bad.add(l.id);
    else {
      bad.delete(l.id);
      if (n === l.quantity) edits.delete(l.id);
      else edits.set(l.id, n);
    }
    this.badQty.set(bad);
    this.qtyEdits.set(edits);
  }

  protected removeLine(l: OrderLine): void {
    this.removed.update((s) => new Set(s).add(l.id));
    this.badQty.update((s) => {
      const n = new Set(s);
      n.delete(l.id);
      return n;
    });
    this.announce.set(`${l.snapshot.variation_code} removed. Save the lines to keep this.`);
  }

  protected resetLines(): void {
    this.qtyEdits.set(new Map());
    this.badQty.set(new Set());
    this.removed.set(new Set());
  }

  protected canSaveLines(): boolean {
    return this.dirtyLines() && this.badQty().size === 0 && this.shownLines().length > 0 && !this.busy();
  }

  protected saveLines(): void {
    const o = this.order();
    if (!o || !this.canSaveLines()) return;
    // A variation already on the order keeps its snapshot; only the quantity changes.
    const items = this.shownLines()
      .filter((l) => l.variation_id !== null)
      .map((l) => ({ variation_id: l.variation_id as number, quantity: this.qty(l) }));
    this.run(this.api.setOrderLines(o.id, items), 'Lines saved.');
  }

  protected canSaveHeader(): boolean {
    const o = this.order();
    if (!o || this.header.invalid || this.busy()) return false;
    const v = this.header.getRawValue();
    return v.name.trim() !== o.name || v.notes.trim() !== o.notes || v.dueDate !== (o.due_date ?? '');
  }

  protected saveHeader(): void {
    const o = this.order();
    if (!o || !this.canSaveHeader()) return;
    const v = this.header.getRawValue();
    this.run(
      this.api.updateOrder(o.id, { name: v.name.trim(), notes: v.notes.trim(), due_date: v.dueDate }),
      'Details saved.',
    );
  }

  protected changeStatus(to: OrderStatus): void {
    const o = this.order();
    if (!o) return;
    const s = STEP[to];
    this.dialog
      .open<OrderStatusDialogComponent, OrderStatusDialogData, string>(OrderStatusDialogComponent, {
        data: { title: s.title, message: s.message, confirmLabel: s.label, danger: to === 'cancelled' },
        width: '460px',
        maxWidth: '95vw',
      })
      .afterClosed()
      .subscribe((note) => {
        if (note === undefined) return;
        this.run(this.api.setOrderStatus(o.id, to, note), `${o.number} is now ${ORDER_STATUS_LABEL[to].toLowerCase()}.`);
      });
  }

  protected cloneOrder(): void {
    const o = this.order();
    if (!o) return;
    this.dialog
      .open<OrderCloneDialogComponent, OrderCloneData, number>(OrderCloneDialogComponent, {
        data: { order: o },
        width: '640px',
        maxWidth: '95vw',
      })
      .afterClosed()
      .subscribe((newId) => {
        if (newId) void this.router.navigate(['/orders', newId]);
      });
  }

  /** Downloads one of the order's printable outputs; the API builds all of them from the order's snapshot. */
  protected download(kind: 'summary' | 'bags' | 'bags-piece' | 'csv'): void {
    const o = this.order();
    if (!o) return;
    const call = {
      summary: () => this.api.orderSummaryPdf(o.id),
      bags: () => this.api.orderJobBagsPdf(o.id, false),
      'bags-piece': () => this.api.orderJobBagsPdf(o.id, true),
      csv: () => this.api.orderCsv(o.id),
    }[kind]();
    const name = {
      summary: `${o.number}-summary.pdf`,
      bags: `${o.number}-jobbags.pdf`,
      'bags-piece': `${o.number}-jobbags-per-piece.pdf`,
      csv: `${o.number}.csv`,
    }[kind];
    this.busy.set(true);
    this.actionError.set('');
    call.subscribe({
      next: (blob) => {
        saveBlob(blob, name);
        this.busy.set(false);
        this.announce.set(`${name} saved`);
      },
      error: async (e) => {
        this.actionError.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }

  protected costOrder(): void {
    const o = this.order();
    if (!o) return;
    this.dialog
      .open<OrderCostDialogComponent, OrderCostData, number>(OrderCostDialogComponent, {
        data: { order: o },
        width: '640px',
        maxWidth: '95vw',
      })
      .afterClosed()
      .subscribe((sheetId) => {
        this.loadSheets();
        if (sheetId) void this.router.navigate(['/costing-sheets', sheetId]);
      });
  }

  private loadSheets(): void {
    const o = this.order();
    if (!o || !this.canWrite()) return;
    this.api.orderCostingSheets(o.id).subscribe({ next: (s) => this.sheets.set(s), error: () => this.sheets.set([]) });
  }

  private run(call: ReturnType<ApiService['order']>, done: string): void {
    this.busy.set(true);
    this.actionError.set('');
    call.subscribe({
      next: (o) => {
        this.setOrder(o);
        this.busy.set(false);
        this.announce.set(done);
        this.loadLive();
      },
      error: async (e) => {
        this.actionError.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }

  protected filesText(l: OrderLine): string {
    const s = l.snapshot;
    return [s.file_3dm && `3dm ${s.file_3dm.name}`, s.file_stl && `stl ${s.file_stl.name}`]
      .filter((x): x is string => !!x)
      .join(' · ');
  }

  protected field(f: string): string {
    return f.replace(/_/g, ' ');
  }
}
