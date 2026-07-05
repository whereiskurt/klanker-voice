'use client';

import {
  Avatar,
  Button,
  Link,
  Navbar,
  NavbarContent,
  NavbarItem,
  Tooltip,
} from '@heroui/react';
import { useSession, signOut } from 'next-auth/react';
import { LogOut } from 'lucide-react';

import { ThemeSwitch } from '../theme-switch';

const basePath = process.env.NODE_ENV === 'production'
  ? `/${process.env.NEXT_PUBLIC_REGION_SHORT || 'use1'}`
  : '';

const APP_VERSION_TOOLTIP = `KlankerMaker Auth ${process.env.NEXT_PUBLIC_VERSION_APP || 'dev'}`;

/**
 * Simplified header (D-09 trim): the run.auth nav (Maps/Meshtastic/Bib) and
 * the Discord/GitHub/Strava login dropdown are dropped entirely — this is a
 * single Email-only identity service, so the only auth action is
 * sign-in/sign-out.
 */
export function Header() {
  const { data: session, status } = useSession();
  const hasSession = status === 'authenticated' && !!session?.user;

  return (
    <Navbar
      maxWidth="lg"
      classNames={{
        base: "glass-nav",
        wrapper: "max-w-[900px]",
      }}
    >
      <NavbarContent justify="center">
        <NavbarItem>
          <Tooltip content={APP_VERSION_TOOLTIP} placement="bottom">
            <Link color="foreground" href="/">
              <span className="font-museo text-lg font-bold tracking-tight">
                klanker<span className="teal-dot">.</span>voice
              </span>
            </Link>
          </Tooltip>
        </NavbarItem>
      </NavbarContent>

      <NavbarContent justify="end" className="gap-2">
        <NavbarItem>
          <ThemeSwitch />
        </NavbarItem>
        <NavbarItem>
          {hasSession ? (
            <div className="flex items-center gap-2">
              <Avatar
                name={session.user?.email || 'U'}
                size="sm"
                isBordered
                color="primary"
              />
              <Button
                variant="light"
                size="sm"
                startContent={<LogOut className="w-4 h-4" />}
                onPress={() => signOut({ callbackUrl: `${basePath}/login` })}
              >
                Sign Out
              </Button>
            </div>
          ) : (
            <Button as="a" href={`${basePath}/login`} variant="ghost" size="sm">
              Sign In
            </Button>
          )}
        </NavbarItem>
      </NavbarContent>
    </Navbar>
  );
}
