import { Component, computed, input, output, signal } from '@angular/core';
import { MatProgressBarModule } from '@angular/material/progress-bar';

export interface StoredFile {
  name: string | null;
  sha256: string | null;
  sizeBytes: number | null;
}

/** A drag-and-drop / click-to-pick zone for one CAD file kind, with its stored SHA status. */
@Component({
  selector: 'app-file-dropzone',
  imports: [MatProgressBarModule],
  template: `
    <div
      class="zone"
      [class.over]="over()"
      [class.disabled]="disabled()"
      role="button"
      [attr.tabindex]="disabled() ? -1 : 0"
      [attr.aria-disabled]="disabled()"
      (click)="pick.click()"
      (keydown.enter)="pick.click()"
      (keydown.space)="pick.click(); $event.preventDefault()"
      (dragover)="onDrag($event, true)"
      (dragleave)="onDrag($event, false)"
      (drop)="onDrop($event)"
    >
      <strong class="mono"><span class="sr-only">Upload </span>.{{ kind() }}</strong>
      <span class="muted">Drop a file here or click to choose</span>
      <input
        #pick
        type="file"
        hidden
        [accept]="'.' + kind()"
        [disabled]="disabled()"
        (change)="onPick(pick)"
      />
    </div>

    @if (progress() !== null) {
      <mat-progress-bar [attr.aria-label]="'Uploading the ' + kind() + ' file'" mode="determinate" [value]="progress()!" />
    }
    @if (error()) {
      <div class="error-box" role="alert">{{ error() }}</div>
    }

    <div class="status" aria-live="polite">
      @if (stored().sha256; as sha) {
        <div class="ok">
          ✓ Stored: {{ stored().name }} · {{ sizeLabel() }}
          <button
            type="button"
            class="remove"
            [disabled]="disabled()"
            [attr.aria-label]="'Remove the stored ' + kind() + ' file'"
            (click)="removed.emit()"
          >
            Remove
          </button>
        </div>
        <div class="mono muted small" [title]="sha">
          sha256 {{ sha.slice(0, 16) }}…{{ sha.slice(-6) }}
        </div>
      } @else {
        <div class="muted">No {{ kind() }} file stored yet</div>
      }
    </div>
  `,
  styles: `
    .zone {
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 4px;
      padding: 20px;
      border: 2px dashed var(--mat-sys-outline);
      border-radius: 12px;
      cursor: pointer;
      text-align: center;
    }
    .zone.over {
      background: var(--mat-sys-surface-container-high);
      border-color: var(--mat-sys-primary);
    }
    .zone.disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }
    .status {
      margin-top: 8px;
    }
    .ok {
      color: var(--mat-sys-primary);
    }
    .remove {
      margin-left: 8px;
      background: none;
      border: none;
      color: var(--mat-sys-error);
      cursor: pointer;
      text-decoration: underline;
      font: inherit;
      min-height: 24px;
      padding: 4px 6px;
    }
    @media (max-width: 720px) {
      .remove {
        min-height: 44px;
        padding: 8px 10px;
      }
    }
    .small {
      font-size: 0.85rem;
      overflow-wrap: anywhere;
    }
  `,
})
export class FileDropzoneComponent {
  readonly kind = input.required<'3dm' | 'stl'>();
  readonly stored = input<StoredFile>({ name: null, sha256: null, sizeBytes: null });
  readonly disabled = input(false);
  /** Upload progress 0-100, or null when idle. */
  readonly progress = input<number | null>(null);
  readonly error = input('');
  readonly picked = output<File>();
  readonly removed = output<void>();

  protected readonly over = signal(false);

  protected readonly sizeLabel = computed(() => {
    const b = this.stored().sizeBytes;
    if (b === null) return '';
    if (b < 1024) return `${b} B`;
    if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
    return `${(b / 1024 / 1024).toFixed(1)} MB`;
  });

  protected onDrag(e: DragEvent, over: boolean): void {
    e.preventDefault();
    if (!this.disabled()) this.over.set(over);
  }

  protected onDrop(e: DragEvent): void {
    e.preventDefault();
    this.over.set(false);
    const f = e.dataTransfer?.files?.[0];
    if (f && !this.disabled()) this.picked.emit(f);
  }

  protected onPick(input: HTMLInputElement): void {
    const f = input.files?.[0];
    if (f) this.picked.emit(f);
    input.value = ''; // allow choosing the same file again
  }
}
