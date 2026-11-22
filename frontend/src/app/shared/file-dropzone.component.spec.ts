import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { FileDropzoneComponent } from './file-dropzone.component';

function mount(stored: { name: string | null; sha256: string | null; sizeBytes: number | null }) {
  const fixture = TestBed.createComponent(FileDropzoneComponent);
  fixture.componentRef.setInput('kind', '3dm');
  fixture.componentRef.setInput('stored', stored);
  fixture.detectChanges();
  return fixture;
}

describe('FileDropzoneComponent', () => {
  it('offers Remove only when a file is stored, and emits removed', () => {
    const empty = mount({ name: null, sha256: null, sizeBytes: null });
    expect(empty.nativeElement.querySelector('.remove')).toBeNull();

    const full = mount({ name: 'ring.3dm', sha256: 'a'.repeat(64), sizeBytes: 2048 });
    let emitted = 0;
    full.componentInstance.removed.subscribe(() => emitted++);
    const btn: HTMLButtonElement = full.nativeElement.querySelector('.remove');
    expect(btn.textContent?.trim()).toBe('Remove');
    btn.click();
    expect(emitted).toBe(1);
  });

  it('takes a .3dm or a .jcd in the native slot and only an .stl in the other', () => {
    const dm = mount({ name: null, sha256: null, sizeBytes: null });
    expect(dm.nativeElement.querySelector('input[type=file]').getAttribute('accept')).toBe('.3dm,.jcd');
    expect(dm.nativeElement.querySelector('strong').textContent).toContain('.3dm / .jcd');

    const stl = TestBed.createComponent(FileDropzoneComponent);
    stl.componentRef.setInput('kind', 'stl');
    stl.detectChanges();
    expect(stl.nativeElement.querySelector('input[type=file]').getAttribute('accept')).toBe('.stl');
  });

  it('disables Remove while the zone is disabled', () => {
    const f = TestBed.createComponent(FileDropzoneComponent);
    f.componentRef.setInput('kind', 'stl');
    f.componentRef.setInput('stored', { name: 'a.stl', sha256: 'b'.repeat(64), sizeBytes: 10 });
    f.componentRef.setInput('disabled', true);
    f.detectChanges();
    expect((f.nativeElement.querySelector('.remove') as HTMLButtonElement).disabled).toBe(true);
  });
});
