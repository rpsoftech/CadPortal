import { Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { ApiService } from '../../core/api.service';
import { SelectedDesign } from '../../core/catalog-selection';
import { toApiError } from '../../core/errors';
import { formatBytes } from '../../core/format';
import { CadZipKind, CadZipLink } from '../../core/models';
import { startDownload } from '../../shared/download';

export interface DownloadCadData {
  kind: CadZipKind;
  designs: SelectedDesign[];
}

/** "STL" / "3dm", the way people write the file types. */
export function kindLabel(kind: CadZipKind): string {
  return kind === 'stl' ? 'STL' : '3dm';
}

/**
 * The sentence shown before a download starts: how many files, how big, from how many variations. With
 * nothing to download it says so, so the button is not offered for an empty ZIP.
 */
export function zipSummary(l: Pick<CadZipLink, 'files' | 'bytes' | 'variations'>, kind: CadZipKind): string {
  if (l.files === 0) return `None of the ${l.variations} variation${l.variations === 1 ? '' : 's'} has a ${kindLabel(kind)} file.`;
  return (
    `${l.files} ${kindLabel(kind)} file${l.files === 1 ? '' : 's'}, ${formatBytes(l.bytes)}, ` +
    `from ${l.variations} variation${l.variations === 1 ? '' : 's'}.`
  );
}

/** What to say about requested files that do not exist; empty when nothing is missing. */
export function missingNote(missing: number, kind: CadZipKind): string {
  if (missing === 0) return '';
  return (
    `${missing} variation${missing === 1 ? ' has' : 's have'} no ${kindLabel(kind)} file. ` +
    `${missing === 1 ? 'It is' : 'They are'} listed as missing in MANIFEST.csv inside the ZIP.`
  );
}

/**
 * Asks the server what the selection holds, shows it, and on Download opens a fresh signed link, which the
 * browser saves as a streamed download (the page never holds the file).
 */
@Component({
  selector: 'app-download-cad-dialog',
  imports: [MatButtonModule, MatDialogModule, MatProgressBarModule],
  template: `
    <h2 mat-dialog-title>Download {{ label }} files</h2>
    <mat-dialog-content class="col">
      <p class="muted">
        One ZIP with the {{ label }} file of every variation of the
        {{ data.designs.length }} selected design{{ data.designs.length === 1 ? '' : 's' }}, named like
        <code>LR0001-V1.{{ data.kind }}</code>, plus a <code>MANIFEST.csv</code> with each file's
        checksum and anything missing.
      </p>
      @if (busy()) {
        <mat-progress-bar mode="indeterminate" [attr.aria-label]="state() === 'loading' ? 'Checking the files' : 'Starting the download'" />
      }
      <div aria-live="polite">
        @if (link(); as l) {
          <p class="summary">{{ summary(l) }}</p>
          @if (note(l); as n) {
            <p class="warn">{{ n }}</p>
          }
        }
        @if (state() === 'started') {
          <p class="ok">
            The download has started. Your browser is saving the ZIP; large selections can take a few
            minutes. If nothing arrives, you may already have a download running: wait for it to
            finish and try again.
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
export class DownloadCadDialogComponent {
  private readonly api = inject(ApiService);
  protected readonly data = inject<DownloadCadData>(MAT_DIALOG_DATA);
  private readonly ref = inject(MatDialogRef<DownloadCadDialogComponent>);

  protected readonly label = kindLabel(this.data.kind);
  protected readonly state = signal<'loading' | 'ready' | 'starting' | 'started' | 'error'>('loading');
  protected readonly link = signal<CadZipLink | null>(null);
  protected readonly error = signal('');
  protected readonly summary = (l: CadZipLink) => zipSummary(l, this.data.kind);
  protected readonly note = (l: CadZipLink) => missingNote(l.missing, this.data.kind);

  constructor() {
    this.ref.disableClose = false;
    this.api.cadZipLink(this.request()).subscribe({
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

  protected canDownload(): boolean {
    const l = this.link();
    return this.state() === 'ready' && !!l && l.files > 0;
  }

  private request() {
    return { design_ids: this.data.designs.map((d) => d.id), kinds: [this.data.kind] };
  }

  /** A link lives two minutes, so a fresh one is taken at the click rather than reusing the first. */
  protected download(): void {
    this.state.set('starting');
    this.api.cadZipLink(this.request()).subscribe({
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
