import { Component, computed, inject, signal } from '@angular/core';
import { rxResource } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ApiService } from '../../core/api.service';
import { FixedPipe } from '../../core/format';
import { Metal } from '../../core/models';
import { MetalDialogComponent } from './master-data-dialogs';
import { RecalcWatcher } from './recalc-watcher';

@Component({
  selector: 'app-metals-tab',
  imports: [MatButtonModule, MatProgressBarModule, FixedPipe],
  template: `
    <div class="row head">
      <p class="muted intro">
        Alloys and their specific gravity (SG). Net weight = volume × SG ÷ 1000. Wastage % feeds the
        costing export; the default input alloy is the one reference weights are usually entered in.
      </p>
      <span class="spacer"></span>
      <button matButton="filled" (click)="edit(null)">Add alloy</button>
    </div>

    @if (metals.isLoading()) {
      <mat-progress-bar aria-label="Loading" mode="indeterminate" />
    }
    @if (metals.error()) {
      <div class="error-box" role="alert">Could not load alloys.</div>
    }
    @if (!hasDefault() && metals.hasValue()) {
      <div class="notice">No default input alloy is set yet.</div>
    }

    <div class="scroll">
      <table class="plain stack" aria-label="Alloys">
        <thead>
          <tr>
            <th scope="col">Code</th>
            <th scope="col" class="num">SG</th>
            <th scope="col" class="num">Wastage %</th>
            <th scope="col">Default input</th>
            <th scope="col">Status</th>
            <th scope="col"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          @for (m of metals.value() ?? []; track m.id) {
            <tr [class.off]="!m.active">
              <td data-label="Code" class="mono">{{ m.code }}</td>
              <td data-label="SG" class="num">{{ m.specific_gravity | fixed: 4 }}</td>
              <td data-label="Wastage %" class="num">
                @if (m.wastage_pct !== null) {
                  {{ m.wastage_pct }}
                } @else {
                  <span class="unset">not set</span>
                }
              </td>
              <td data-label="Default input">{{ m.is_default_input ? 'Yes' : '' }}</td>
              <td data-label="Status">{{ m.active ? 'Active' : 'Inactive' }}</td>
              <td class="row-actions">
                <button matButton (click)="edit(m)" [attr.aria-label]="'Edit ' + m.code">Edit</button>
              </td>
            </tr>
          }
        </tbody>
      </table>
    </div>
  `,
  styles: `
    .head {
      margin-bottom: 8px;
    }
    .intro {
      flex: 1;
      min-width: 260px;
      margin: 0;
    }
    .spacer {
      flex: 0;
    }
    .scroll {
      overflow-x: auto;
    }
    tr.off td {
      color: var(--mat-sys-on-surface-variant);
    }
    .unset {
      color: var(--mat-sys-error);
    }
    .notice {
      margin: 8px 0;
      padding: 8px 12px;
      border-radius: 8px;
      background: var(--mat-sys-surface-container);
    }
  `,
})
export class MetalsTabComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snack = inject(MatSnackBar);
  private readonly recalc = inject(RecalcWatcher);

  private readonly tick = signal(0);
  protected readonly metals = rxResource({
    params: () => this.tick(),
    stream: () => this.api.metals(),
  });
  protected readonly hasDefault = computed(() =>
    (this.metals.value() ?? []).some((m) => m.is_default_input && m.active),
  );

  protected edit(metal: Metal | null): void {
    this.dialog
      .open<MetalDialogComponent, Metal | null, Metal>(MetalDialogComponent, {
        data: metal,
        width: '440px',
      })
      .afterClosed()
      .subscribe((saved) => {
        if (!saved) return;
        this.snack.open(`Saved ${saved.code}. Weights are being rebuilt.`, 'OK', {
          duration: 4000,
        });
        this.tick.update((n) => n + 1);
        this.recalc.watch();
      });
  }
}
