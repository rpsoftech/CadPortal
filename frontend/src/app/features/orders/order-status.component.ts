import { Component, computed, input } from '@angular/core';
import { ORDER_STATUS_LABEL, OrderStatus } from '../../core/models';

/** The status of an order as a labelled chip: the words carry the meaning, the colour only helps. */
@Component({
  selector: 'app-order-status',
  template: `<span class="chip" [class]="'chip ' + status()">{{ label() }}</span>`,
  styles: `
    .chip {
      display: inline-block;
      padding: 2px 10px;
      border-radius: 999px;
      border: 1px solid var(--mat-sys-outline);
      font-size: 0.85rem;
      font-weight: 600;
      white-space: nowrap;
      color: var(--mat-sys-on-surface);
    }
    .placed,
    .in_production {
      background: var(--mat-sys-secondary-container);
      color: var(--mat-sys-on-secondary-container);
      border-color: transparent;
    }
    .done {
      background: var(--mat-sys-primary-container);
      color: var(--mat-sys-on-primary-container);
      border-color: transparent;
    }
    .cancelled {
      background: var(--mat-sys-surface-container-high);
      color: var(--mat-sys-on-surface-variant);
      text-decoration: line-through;
    }
  `,
})
export class OrderStatusComponent {
  readonly status = input.required<OrderStatus>();
  protected readonly label = computed(() => ORDER_STATUS_LABEL[this.status()]);
}
