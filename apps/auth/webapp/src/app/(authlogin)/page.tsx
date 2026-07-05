'use client';

import { Card, CardBody, Button, Divider, Avatar } from '@heroui/react';
import { useEffect, useState } from 'react';
import { useSession, signOut } from 'next-auth/react';
import { LogOut } from 'lucide-react';

const basePath = process.env.NODE_ENV === 'production'
  ? `/${process.env.NEXT_PUBLIC_REGION_SHORT || 'use1'}`
  : '';

/**
 * Minimal home/status page (D-09 trim): run.auth's dashboard rendered
 * linked-OAuth-provider status, quota tier, and a "Full Profile" page — all
 * dropped for klanker-voice (Email-only, no quota code this phase, D-11).
 * This just confirms sign-in state for the KlankerMaker concierge identity
 * service; the voice client (Phase 5) is the real destination after login.
 */
function HomeContent() {
  const { data: session, status } = useSession();

  if (status === 'loading') {
    return (
      <div className="glass-card rounded-xl p-6">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-full bg-content2 animate-pulse" />
          <div className="space-y-2 flex-1">
            <div className="h-5 w-32 rounded bg-content2 animate-pulse" />
            <div className="h-4 w-48 rounded bg-content2 animate-pulse" />
          </div>
        </div>
      </div>
    );
  }

  if (status === 'unauthenticated' || !session) {
    return (
      <div className="space-y-6 animate-fade-up">
        <div className="text-center space-y-2">
          <h1 className="font-museo text-4xl font-bold tracking-tight text-foreground">
            klanker<span className="teal-dot">.</span>voice
          </h1>
          <p className="font-mono text-xs text-default-400 tracking-widest uppercase">
            KlankerMaker Concierge — Identity Service
          </p>
        </div>
        <Card className="glass-card">
          <CardBody className="flex justify-center py-6">
            <Button as="a" href={`${basePath}/login`} variant="solid" color="primary" className="font-semibold" size="lg">
              Sign In
            </Button>
          </CardBody>
        </Card>
      </div>
    );
  }

  const { user } = session;

  return (
    <div className="space-y-4 w-full animate-fade-up">
      <Card className="glass-card overflow-hidden">
        <CardBody className="px-5 py-5">
          <div className="flex items-center gap-4">
            <Avatar
              name={user?.name || user?.email || 'U'}
              size="lg"
              isBordered
              color="primary"
            />
            <div className="flex flex-col min-w-0 flex-1">
              <span className="font-museo text-xl font-bold text-foreground truncate">
                {user?.name || 'Signed in'}
              </span>
              <span className="text-sm text-default-500 truncate">{user?.email}</span>
            </div>
          </div>
        </CardBody>
        <Divider />
        <CardBody className="px-5 py-4">
          <Button
            variant="flat"
            color="danger"
            className="w-full"
            startContent={<LogOut className="w-4 h-4" />}
            onPress={() => signOut({ callbackUrl: `${basePath}/login` })}
          >
            Sign Out
          </Button>
        </CardBody>
      </Card>
    </div>
  );
}

export default function HomePage() {
  const [mounted, setMounted] = useState(false);
  useEffect(() => { setMounted(true); }, []);

  if (!mounted) {
    return (
      <div className="glass-card rounded-xl p-6">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-full bg-content2 animate-pulse" />
          <div className="space-y-2 flex-1">
            <div className="h-5 w-32 rounded bg-content2 animate-pulse" />
            <div className="h-4 w-48 rounded bg-content2 animate-pulse" />
          </div>
        </div>
      </div>
    );
  }

  return <HomeContent />;
}
