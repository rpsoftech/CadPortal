import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { AuthService } from '../core/auth.service';

@Component({
  selector: 'app-token-dialog',
  imports: [FormsModule, MatDialogModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  template: `
    <h2 mat-dialog-title>Bearer token</h2>
    <mat-dialog-content>
      <p class="muted">
        There is no login service yet. Paste a token issued for your role (admin, designer or
        production). It is kept for this browser tab only.
      </p>
      <mat-form-field appearance="outline" style="width: 100%">
        <mat-label>Token</mat-label>
        <textarea matInput rows="4" [(ngModel)]="value" class="mono" spellcheck="false"></textarea>
      </mat-form-field>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button (click)="clear()">Sign out</button>
      <button mat-button mat-dialog-close>Cancel</button>
      <button mat-flat-button (click)="save()" [disabled]="!value.trim()">Save</button>
    </mat-dialog-actions>
  `,
})
export class TokenDialogComponent {
  private readonly auth = inject(AuthService);
  private readonly ref = inject(MatDialogRef<TokenDialogComponent>);
  value = this.auth.token() ?? '';

  save(): void {
    this.auth.set(this.value);
    this.ref.close();
  }

  clear(): void {
    this.auth.clear();
    this.ref.close();
  }
}
