import unittest
from types import SimpleNamespace
from tools import openai_oauth_reauth_worker as worker

class ExcelWorkerScopeTests(unittest.TestCase):
    def test_rejects_other_profiles_and_engines_before_login(self):
        for patch in ({'oauth_profile':'codex'}, {'oauth_profile':'siwc'}, {'engine':'session_studio'}, {'credential_mode':'email_otp_url'}):
            claim={'oauth_profile':'excel','engine':'local_worker','credential_mode':'password_totp',**patch}
            with self.assertRaises(worker.WorkerError):
                worker.process_password_claim(SimpleNamespace(),claim)

    def test_worker_claims_only_supported_api(self):
        api=worker.WorkerAPI(SimpleNamespace(worker_id='fixture'))
        calls=[]
        api._post=lambda path,payload: calls.append((path,payload))
        self.assertFalse(worker.run_once(api))
        self.assertEqual(calls,[('/api/v1/internal/openai-reauth/claim',{'worker_id':'fixture','engines':['local_worker']})])
        self.assertFalse(hasattr(api,'claim_totp'))
        self.assertFalse(hasattr(api,'claim_session_export'))

    def test_failure_messages_never_echo_provider_secrets(self):
        secret='unlabelled-secret-password-and-session'
        self.assertEqual(worker.safe_worker_failure(RuntimeError(secret)),'worker_failed')
        message='Excel password/TOTP login failed (stage=mfa_challenge, reason=security_check, exit=1)'
        self.assertEqual(worker.safe_worker_failure(worker.WorkerError(message)),'security_check')
        self.assertEqual(worker.safe_worker_failure(worker.WorkerError(message+' '+secret)),'worker_failed')

if __name__=='__main__': unittest.main()
