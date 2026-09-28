import * as RadixCheckbox from '@radix-ui/react-checkbox';
import { Label } from '@radix-ui/react-label';
import { Check } from 'lucide-react';
import { useId } from 'react';
import type { ReactNode } from 'react';

export interface CheckboxProps {
  label: ReactNode;
  hint?: string;
  checked?: boolean;
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  disabled?: boolean;
  name?: string;
}

export function Checkbox({ label, hint, ...rest }: CheckboxProps) {
  const id = useId();
  return (
    <div className="check">
      <RadixCheckbox.Root id={id} className="check-box" aria-describedby={hint ? `${id}-hint` : undefined} {...rest}>
        <RadixCheckbox.Indicator>
          <Check size={12} strokeWidth={3} aria-hidden="true" />
        </RadixCheckbox.Indicator>
      </RadixCheckbox.Root>
      <div className="stack">
        <Label htmlFor={id}>{label}</Label>
        {hint ? (
          <span id={`${id}-hint`} className="hint">
            {hint}
          </span>
        ) : null}
      </div>
    </div>
  );
}
