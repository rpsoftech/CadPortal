import { Component, computed, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { ApiService } from '../../core/api.service';
import { parseDesignCsv } from '../../core/design-csv';
import { toApiError } from '../../core/errors';
import { DesignImportResult } from '../../core/models';

/** Bulk-creates designs and their variations from pasted or uploaded CSV. Closes with the design count. */
@Component({
  selector: 'app-design-import-dialog',
  imports: [MatDialogModule, MatButtonModule],
  template: `
    <h2 mat-dialog-title>Import designs</h2>
    <mat-dialog-content class="col wide">
      <p class="muted">
        Paste rows copied from a spreadsheet, or choose a CSV file. Columns, in order:
        <span class="mono">code, name, category, tags, volume_mm3, ref_weight_g, ref_metal</span>.
        Put several tags in one cell separated by <span class="mono">;</span> (write
        <span class="mono">Group:Name</span> if a tag name exists in two groups). Give each
        variation either a volume, or a reference weight with its alloy. Repeat a code on another
        row to add a variation to the same design. Categories, tags and alloys must already exist,
        and existing designs are never changed.
      </p>
      <input type="file" accept=".csv,.tsv,.txt,text/csv" (change)="onFile($event)" />
      <textarea
        class="paste mono"
        rows="8"
        spellcheck="false"
        placeholder="LR001,Lotus ring,Rings,Floral;Bridal,200,,"
        [value]="text()"
        (input)="setText($any($event.target).value)"
        aria-label="Design data"
      ></textarea>

      @if (text().trim()) {
        @if (parsed().errors.length) {
          <div class="error-box">
            {{ parsed().errors.length }} problem{{ parsed().errors.length === 1 ? '' : 's' }} in the
            file layout
          </div>
          <ul class="errors">
            @for (e of parsed().errors.slice(0, 15); track e) {
              <li>{{ e }}</li>
            }
          </ul>
        } @else if (checked(); as c) {
          @if (c.errors.length) {
            <div class="error-box" role="alert">
              {{ c.errors.length }} problem{{ c.errors.length === 1 ? '' : 's' }}. Nothing is
              imported until all are fixed.
            </div>
            <ul class="errors">
              @for (e of c.errors.slice(0, 30); track $index) {
                <li>Line {{ e.line }}: {{ e.message }}</li>
              }
            </ul>
            @if (c.errors.length > 30) {
              <p class="muted">…and {{ c.errors.length - 30 }} more.</p>
            }
          } @else {
            <div class="ok" role="status">
              Ready: {{ c.designs }} design{{ c.designs === 1 ? '' : 's' }} and
              {{ c.variations }} variation{{ c.variations === 1 ? '' : 's' }} will be created.
            </div>
          }
        } @else {
          <p class="muted">{{ parsed().rows.length }} rows read. Check them before importing.</p>
        }
      }
      @if (error()) {
        <div class="error-box" role="alert">{{ error() }}</div>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton (click)="check()" [disabled]="!canCheck() || busy()">Check</button>
      <button matButton="filled" (click)="save()" [disabled]="!canImport() || busy()">
        Import
      </button>
    </mat-dialog-actions>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .wide {
      min-width: min(680px, 80vw);
    }
    .paste {
      width: 100%;
      box-sizing: border-box;
      padding: 8px;
      border-radius: 8px;
      border: 1px solid var(--mat-sys-outline);
      background: transparent;
      color: inherit;
    }
    .ok {
      color: var(--mat-sys-primary);
    }
    .errors {
      margin: 0;
      padding-left: 20px;
      max-height: 200px;
      overflow: auto;
    }
  `,
})
export class DesignImportDialogComponent {
  private readonly ref = inject(MatDialogRef<DesignImportDialogComponent, number>);
  private readonly api = inject(ApiService);

  protected readonly text = signal('');
  protected readonly busy = signal(false);
  protected readonly error = signal('');
  /** The server's verdict on the current text; cleared whenever the text changes. */
  protected readonly checked = signal<DesignImportResult | null>(null);

  protected readonly parsed = computed(() => parseDesignCsv(this.text()));
  protected readonly canCheck = computed(
    () => this.parsed().rows.length > 0 && this.parsed().errors.length === 0,
  );
  protected readonly canImport = computed(() => {
    const c = this.checked();
    return this.canCheck() && c !== null && c.errors.length === 0 && c.designs > 0;
  });

  protected setText(value: string): void {
    this.text.set(value);
    this.checked.set(null);
    this.error.set('');
  }

  protected onFile(ev: Event): void {
    const file = (ev.target as HTMLInputElement).files?.[0];
    if (!file) return;
    if (file.size > 2_000_000) {
      this.error.set('That file is too large for a design import.');
      return;
    }
    void file.text().then((t) => this.setText(t));
  }

  protected check(): void {
    if (!this.canCheck()) return;
    this.run(true, (res) => this.checked.set(res));
  }

  protected save(): void {
    if (!this.canImport()) return;
    this.run(false, (res) => {
      if (res.errors.length)
        this.checked.set(res); // changed since the check, e.g. a code taken
      else this.ref.close(res.designs);
    });
  }

  private run(dryRun: boolean, done: (r: DesignImportResult) => void): void {
    this.busy.set(true);
    this.error.set('');
    this.api.importDesigns(this.parsed().rows, dryRun).subscribe({
      next: (res) => {
        this.busy.set(false);
        done(res);
      },
      error: async (e) => {
        this.error.set((await toApiError(e)).message);
        this.busy.set(false);
      },
    });
  }
}
