import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { updateProfile } from '../../lib/authApi';
import type { User } from '../../lib/authApi';
import { describeError } from '../../lib/http';
import { useSettingsUser } from './SettingsLayout';

// Profilo: username ed email in sola lettura, nome visualizzato e bio
// modificabili (PATCH /users/{username}).
export function ProfilePage() {
  const initial = useSettingsUser();
  const { toast } = useToast();
  const [user, setUser] = useState<User>(initial);
  const [displayName, setDisplayName] = useState(initial.displayName);
  const [bio, setBio] = useState(initial.bio ?? '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    setError(null);
    try {
      const updated = await updateProfile(user.username, { displayName: displayName.trim(), bio });
      setUser(updated);
      toast('Profile saved.', 'success');
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div>
      <div className="sec-h">
        <h2>Profile</h2>
      </div>
      <form className="card" onSubmit={(e) => void handleSubmit(e)}>
        <div className="card-b stack form-stack">
          {error ? <ErrorAlert message={error} /> : null}
          <FormField label="Username" hint="The username cannot be changed.">
            <TextInput value={user.username} readOnly />
          </FormField>
          {user.email ? (
            <FormField label="Email">
              <TextInput value={user.email} readOnly />
            </FormField>
          ) : null}
          <FormField label="Display name">
            <TextInput value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
          </FormField>
          <FormField label="Bio">
            <textarea className="input textarea" rows={3} value={bio} onChange={(e) => setBio(e.target.value)} />
          </FormField>
          <div className="row">
            <span className="sp" />
            <Button type="submit" variant="primary" disabled={saving}>
              {saving ? 'Saving…' : 'Save profile'}
            </Button>
          </div>
        </div>
      </form>
    </div>
  );
}
