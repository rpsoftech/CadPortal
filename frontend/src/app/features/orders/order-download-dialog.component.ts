import { Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { ApiService } from '../../core/api.service';
import { toApiError } from '../../core/errors';
import { formatBytes } from '../../core/format';
import { CadZipKind, Order, OrderZipLink } from '../../core/models';
import { startDownload } from '../../shared/download';

/** What the dialog fetches: the STL ZIP, the 3dm ZIP, or the package (both kinds plus job bags and summary). */
export type OrderDownloadWhat = 'stl' | '3dm' | 'package';

export interface OrderDownloadData {
  order: Order;
  what: OrderDownloadWhat;
}

/** The file kinds an option collects; the package carries both. */
export function orderKinds(what: OrderDownloadWhat): CadZipKind[] {
  return what === 'package' ? ['stl', '3dm'] : [what];
}

/** "STL files", "3dm files" or "STL and 3dm files". */
function kindsText(what: OrderDownloadWhat): string {
  return what === 'package' ? 'STL and 3dm files' : what === 'stl' ? 'STL files' : '3dm files';
}

/** Does any line of the order record a file of this kind? Decides whether the button is offered. */
export function orderHasFiles(order: Order, what: OrderDownloadWhat): boolean {
  return order.lines.some((l) => {
    const s = l.snapshot;
    return (what !== '3dm' && !!s.file_stl) || (what !== 'stl' && !!s.file_3dm);
  });
}

/** The sentence shown before a download starts: how many files, how big, from how many lines. */
export function orderZipSummary(l: Pick<OrderZipLink, 'files' | 'bytes' | 'lines' | 'package'>, what: OrderDownloadWhat): string {
  const lines = `${l.lines} line${l.lines === 1 ? '' : 's'}`;
  if (l.files === 0) {
    return l.package
      ? `No line has a CAD file. The package still holds the job bags and the summary, for ${lines}.`
      : `None of the ${lines} has ${what === 'stl' ? 'an STL' : 'a 3dm'} file.`;
  }
  return `${l.files} CAD file${l.files === 1 ? '' : 's'} (${kindsText(what)}), ${formatBytes(l.bytes)}, from ${lines}.`;
}

/** What to say about requested files the order did not record or that changed since; empty when neither. */
export function orderZipNotes(l: Pick<OrderZipLink, 'missing' | 'changed'>): string[] {
  const notes: string[] = [];
  if (l.missing > 0) {
    notes.push(`${l.missing} requested file${l.missing === 1 ? ' is' : 's are'} not on the order. ${l.missing === 1 ? 'It is' : 'They are'} listed as missing in MANIFEST.csv inside the ZIP.`);
  }
  if (l.changed > 0) {
    notes.push(
      `${l.changed} file${l.changed === 1 ? ' was' : 's were'} replaced or removed in the catalog since the order was made. ` +
        `The ZIP holds the file the order recorded; MANIFEST.csv marks it.`,
    );
  }
  return notes;
}

/**
 * Asks the server what the order's download holds, shows it, and on Download opens a fresh signed link,
 * which the browser saves as a streamed download (the page never holds the file).
 */
@Component({
  selector: 'app-order-download-dialog',
  imports: [MatButtonModule, MatDialogModule, MatProgressBarModule],
  template: `
    <h2 mat-dialog-title>{{ title }}</h2>
    <mat-dialog-content class="col">
      <p class="muted">{{ intro }}</p>
      @if (busy()) {
        <mat-progress-bar mode="indeterminate" [attr.aria-label]="state() === 'loading' ? 'Checking the files' : 'Starting the download'" />
      }
      <div aria-live="polite">
        @if (link(); as l) {
          <p class="summary">{{ summary(l) }}</p>
          @for (n of notes(l); track n) {
            <p class="warn">{{ n }}</p>
          }
        }
        @if (state() === 'started') {
          <p class="ok">
            The download has started. Your browser is saving the ZIP; large orders can take a few minutes.
            If nothing arrives, you may already have a download running: wait for it to finish and try
            again.
          </p>
        }
      </div>
      @if (error()) {
        <div class="error-box" role="alert">{{ error() }}</div>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton type="button" mat-dialog-close>{{ state() === 'started' ? 'Close' : 'Cancel' }}</button>
      @if (state() !== 'started') {
        <button matButton="filled" type="button" [disabled]="!canDownload()" (click)="download()">
          {{ state() === 'starting' ? 'Starting…' : 'Download ZIP' }}
        </button>
      }
    </mat-dialog-actions>
  `,
  styles: `
    .col {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .summary {
      font-weight: 600;
    }
    .warn {
      padding: 8px 12px;
      border-radius: 8px;
      background: var(--mat-sys-tertiary-container);
      color: var(--mat-sys-on-tertiary-container);
    }
    .ok {
      color: var(--mat-sys-primary);
    }
  `,
})
export class OrderDownloadDialogComponent {
  private readonly api = inject(ApiService);
  protected readonly data = inject<OrderDownloadData>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<OrderDownloadDialogComponent>);

  protected readonly title =
    this.data.what === 'package' ? 'Download order package' : `Download ${this.data.what === 'stl' ? 'STL' : '3dm'} files`;
  protected readonly intro =
    this.data.what === 'package'
      ? `One ZIP for ${this.data.order.number}: the job bags PDF, a summary CSV, the STL and 3dm files the order recorded, and a MANIFEST.csv with each file's checksum and anything missing.`
      : `One ZIP for ${this.data.order.number} with the ${this.data.what === 'stl' ? 'STL' : '3dm'} file each line recorded when the order was made, plus a MANIFEST.csv with each file's checksum and anything missing.`;

  protected readonly state = signal<'loading' | 'ready' | 'starting' | 'started' | 'error'>('loading');
  protected readonly link = signal<OrderZipLink | null>(null);
  protected readonly error = signal('');
  protected readonly summary = (l: OrderZipLink) => orderZipSummary(l, this.data.what);
  protected readonly notes = (l: OrderZipLink) => orderZipNotes(l);

  constructor() {
    this.ref.disableClose = false;
    this.fetch().subscribe({
      next: (l) => {
        this.link.set(l);
        this.state.set('ready');
      },
      error: (e) => this.fail(e),
    });
  }

  protected busy(): boolean {
    return this.state() === 'loading' || this.state() === 'starting';
  }

  /** The package is useful without CAD files (job bags, summary); a plain ZIP is not. */
  protected canDownload(): boolean {
    const l = this.link();
    return this.state() === 'ready' && !!l && (l.files > 0 || l.package);
  }

  private fetch() {
    return this.api.orderZipLink(this.data.order.id, orderKinds(this.data.what), this.data.what === 'package');
  }

  /** A link lives two minutes, so a fresh one is taken at the click rather than reusing the first. */
  protected download(): void {
    this.state.set('starting');
    this.fetch().subscribe({
      next: (l) => {
        this.link.set(l);
        startDownload(l.url, l.filename);
        this.state.set('started');
      },
      error: (e) => this.fail(e),
    });
  }

  private async fail(e: unknown): Promise<void> {
    this.error.set((await toApiError(e)).message);
    this.state.set('error');
  }
}
