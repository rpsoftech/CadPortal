import { Component, inject, signal } from '@angular/core';
import { FormControl, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { Order, OrderDiff, OrderProblem } from '../../core/models';

export interface OrderCloneData {
  order: Order;
}

/** Closes with the id of the new order when the person opens it, otherwise with nothing. */
@Component({
  selector: 'app-order-clone-dialog',
  imports: [
    ReactiveFormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatProgressBarModule,
    MatSlideToggleModule,
  ],
  template: `
    @if (!result()) {
      <h2 mat-dialog-title>Order again: {{ data.order.number }}</h2>
      <mat-dialog-content class="col">
        <p>
          Makes a new draft with the same quantities, in {{ data.order.metal_code }} ·
          {{ data.order.stone_code }}, using the designs as they are <strong>today</strong>. Afterwards you
          see what differs from the old order.
        </p>
        <mat-form-field appearance="outline">
          <mat-label>Title / buyer</mat-label>
          <input matInput [formControl]="name" maxlength="200" autocomplete="off" />
        </mat-form-field>
        <mat-slide-toggle [formControl]="skip">
          Leave out lines that can no longer be ordered (they are listed)
        </mat-slide-toggle>
        @if (busy()) {
          <mat-progress-bar mode="indeterminate" aria-label="Making the new order" />
        }
        @if (error(); as e) {
          <div class="error-box" role="alert">
            {{ e }}
            @if (problems().length > 0) {
              <ul>
                @for (p of problems(); track $index) {
                  <li>
                    <span class="mono">{{ p.variation_code || '#' + p.variation_id }}</span>: {{ p.message }}
                  </li>
                }
              </ul>
            }
          </div>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton mat-dialog-close>Cancel</button>
        <button matButton="filled" (click)="clone()" [disabled]="busy() || name.invalid">Make new draft</button>
      </mat-dialog-actions>
    } @else {
      <h2 mat-dialog-title>{{ result()!.order.number }} created</h2>
      <mat-dialog-content class="col">
        <p role="status">
          {{ result()!.diff.unchanged }} line{{ result()!.diff.unchanged === 1 ? '' : 's' }} unchanged,
          {{ result()!.diff.changed }} changed, {{ result()!.diff.unavailable }} left out.
        </p>
        @for (l of changedLines(); track $index) {
          <section class="diff" [attr.aria-label]="l.variation_code + ' ' + l.status">
            <h3>
              <span class="mono">{{ l.design_code }}-{{ l.variation_code }}</span>
              <span class="muted"> · {{ l.status === 'changed' ? 'changed' : 'left out' }}</span>
            </h3>
            @if (l.reason) {
              <p>{{ l.reason }}</p>
            }
            <ul>
              @for (c of l.changes; track c.field) {
                <li>
                  {{ label(c.field) }}: <del>{{ c.old }}</del> → <ins>{{ c.new }}</ins>
                </li>
              }
            </ul>
          </section>
        } @empty {
          <p class="muted">Nothing changed since the old order was made.</p>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button matButton mat-dialog-close>Stay here</button>
        <button matButton="filled" [mat-dialog-close]="result()!.order.id" cdkFocusInitial>
          Open {{ result()!.order.number }}
        </button>
      </mat-dialog-actions>
    }
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
      min-width: min(560px, 80vw);
    }
    .diff h3 {
      margin: 8px 0 0;
      font-size: 1rem;
    }
    .diff ul {
      margin: 4px 0;
      padding-left: 20px;
      overflow-wrap: anywhere;
    }
    ins {
      text-decoration: none;
      font-weight: 600;
    }
    @media (max-width: 720px) {
      .col {
        min-width: 0;
      }
    }
  `,
})
export class OrderCloneDialogComponent {
  protected readonly data = inject<OrderCloneData>(MAT_DIALOG_DATA);
  private readonly api = inject(ApiService);

  protected readonly name = new FormControl(this.data.order.name, {
    nonNullable: true,
    validators: Validators.maxLength(200),
  });
  protected readonly skip = new FormControl(false, { nonNullable: true });
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  protected readonly problems = signal<OrderProblem[]>([]);
  protected readonly result = signal<{ order: Order; diff: OrderDiff } | null>(null);

  protected changedLines() {
    return (this.result()?.diff.lines ?? []).filter((l) => l.status !== 'unchanged');
  }

  protected label(field: string): string {
    return field.replace(/_/g, ' ');
  }

  protected clone(): void {
    this.busy.set(true);
    this.error.set('');
    this.problems.set([]);
    this.api
      .cloneOrder(this.data.order.id, { name: this.name.value.trim(), skip_unavailable: this.skip.value })
      .subscribe({
        next: (r) => {
          this.result.set(r);
          this.busy.set(false);
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
