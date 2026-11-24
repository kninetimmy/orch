# Test-only SSH transport. Git still sends its normal upload/receive-pack
# protocol, but only these synthetic repository names can reach a local bare repo.
set -eu
if [ "$#" -eq 2 ] && [ "$1" = git@github.example.invalid ]; then
    service=${2%% *}
    case "$2" in
        "git-upload-pack 'fixture/orch-smoke.git'"|"git-receive-pack 'fixture/orch-smoke.git'")
            exec git "${service#git-}" "${ORCH_CLEANUP_FIXTURE_REMOTE:?}"
            ;;
        "git-upload-pack 'fixture/other.git'"|"git-receive-pack 'fixture/other.git'")
            exec git "${service#git-}" "${ORCH_CLEANUP_FIXTURE_OTHER:?}"
            ;;
    esac
fi
echo 'cleanup SSH fixture refused unexpected command' >&2
exit 1
