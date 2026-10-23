import { Component, Signal, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';

export interface IdleWarningData {
  secondsLeft: Signal<number | null>;
  stay: () => void;
}

/** "You will be signed out in 60 s": shown shortly before the idle timeout. */
@Component({
  selector: 'app-idle-warning-dialog',
  imports: [MatDialogModule, MatButtonModule],
  template: `
    <h2 mat-dialog-title>Still there?</h2>
    <mat-dialog-content>
      <p role="alert" data-testid="idle-countdown">
        You will be signed out in {{ data.secondsLeft() }} s because there has been no activity.
      </p>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton="filled" cdkFocusInitial (click)="data.stay()">Stay signed in</button>
    </mat-dialog-actions>
  `,
})
export class IdleWarningDialogComponent {
  protected readonly data = inject<IdleWarningData>(MAT_DIALOG_DATA);
}
