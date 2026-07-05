'use client';

import {
  addToast,
  Button,
  InputOtp,
  ToastProvider,
  Card,
  CardBody,
  Divider,
} from '@heroui/react';

import { useSearchParams } from 'next/navigation';
import { useEffect, useState, Suspense } from 'react';
import { FaMobileScreenButton } from 'react-icons/fa6';
import { Mail, ArrowLeft } from 'lucide-react';
import React from 'react';

const basePath = process.env.NODE_ENV === 'production'
  ? `/${process.env.NEXT_PUBLIC_REGION_SHORT || 'use1'}`
  : '';

function EmailVerificationForm() {
  const searchParams = useSearchParams();
  const [email, setEmail] = useState<string>('');
  const [code, setCode] = useState<string>('');
  const oidcInteraction = searchParams?.get('oidc');

  useEffect(() => {
    addToast({
      title: 'Email Sent',
      description: 'Check your inbox for a verification code.',
      color: 'success',
      variant: 'flat',
    });

    const emailQuery = searchParams
      ?.get('email')
      ?.replace(' ', '%2B')
      .replace('+', '%2B');

    setEmail(emailQuery || '');
  }, [searchParams]);

  const getCallbackUrl = () => {
    if (oidcInteraction) {
      return `${basePath}/api/oidc/interaction/${oidcInteraction}`;
    }
    return `${basePath}/`;
  };

  const handleValidation = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const callbackUrl = encodeURIComponent(getCallbackUrl());
    const url = `${basePath}/api/auth/callback/nodemailer?token=${code}&email=${email}&callbackUrl=${callbackUrl}`;
    window.location.href = url;
    return false;
  };

  const handlePress = (e: any) => {
    if (e && typeof e.preventDefault === 'function') {
      e.preventDefault();
    }
    const callbackUrl = encodeURIComponent(getCallbackUrl());
    const url = `${basePath}/api/auth/callback/nodemailer?token=${code}&email=${email}&callbackUrl=${callbackUrl}`;
    window.location.href = url;
  };

  return (
    <div className="space-y-6 animate-fade-up">
      {/* Wordmark */}
      <div className="text-center space-y-2">
        <h1 className="font-museo text-4xl font-bold tracking-tight text-foreground">
          klanker<span className="teal-dot">.</span>voice
        </h1>
      </div>

      <Card className="glass-card overflow-hidden">
        <CardBody className="space-y-5 px-5 py-5">
          {/* Status header */}
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-lg bg-success/10 flex items-center justify-center flex-shrink-0">
              <Mail className="w-5 h-5 text-success" />
            </div>
            <div>
              <h2 className="font-museo text-lg font-bold text-foreground">Check your email</h2>
              {email && (
                <p className="text-sm text-default-500">
                  Sent to <span className="font-mono text-foreground">{email.replace('%2B', '+')}</span>
                </p>
              )}
            </div>
          </div>

          <Divider />

          {/* Code input */}
          <form onSubmit={handleValidation} className="space-y-4">
            <div className="space-y-2">
              <label className="block text-sm font-medium text-default-600">
                Verification Code
              </label>
              <div className="flex justify-center">
                <InputOtp
                  autoFocus={true}
                  name="code"
                  type="code"
                  placeholder="XXXXXX"
                  length={6}
                  value={code}
                  onChange={(e) => setCode((e.target as HTMLInputElement).value)}
                />
              </div>
            </div>

            <Button
              type="submit"
              variant="solid"
              color="primary"
              className="w-full font-semibold"
              onPress={handlePress}
              startContent={<FaMobileScreenButton className="w-4 h-4" />}
            >
              Verify Code
            </Button>
          </form>

          <div className="text-center">
            <Button
              as="a"
              href={`${basePath}/login`}
              variant="light"
              size="sm"
              className="text-default-400"
              startContent={<ArrowLeft className="w-3.5 h-3.5" />}
            >
              Back to login
            </Button>
          </div>
        </CardBody>
      </Card>
    </div>
  );
}

export default function EmailLogin() {
  return (
    <>
      <ToastProvider placement="bottom-center" />
      <Suspense
        fallback={
          <div className="space-y-6">
            <div className="text-center">
              <div className="h-10 w-48 mx-auto rounded bg-content2 animate-pulse" />
            </div>
            <div className="glass-card rounded-xl p-6">
              <div className="h-32 rounded bg-content2 animate-pulse" />
            </div>
          </div>
        }
      >
        <EmailVerificationForm />
      </Suspense>
    </>
  );
}
