#!/usr/bin/env bash
# Vendors the frontend runtime into static/ from pinned jsDelivr URLs.
# Every asset is sha256-verified: a CDN surprise must never reach production.
# The results are COMMITTED to the repo — this script only runs on upgrades.
set -euo pipefail

fetch() { # name url dest sha256
  local name="$1" url="$2" dest="$3" sha="$4"
  echo "fetching ${name} → ${dest}"
  curl -sfL -o "${dest}.tmp" "${url}"
  if ! echo "${sha}  ${dest}.tmp" | shasum -a 256 -c - >/dev/null 2>&1; then
    rm -f "${dest}.tmp"
    echo "sha256 mismatch for ${name} (${url}) — refusing to install" >&2
    exit 1
  fi
  mv "${dest}.tmp" "${dest}"
}

mkdir -p static/vendor static/fonts

fetch "htmx.org@4.0.0-beta6" \
  "https://cdn.jsdelivr.net/npm/htmx.org@4.0.0-beta6/dist/htmx.min.js" \
  "static/vendor/htmx.min.js" \
  "28fae7bbe8e8142b702debb9d5234a9a436d9435a4b5165b195aa1a7ed840d25"

fetch "htmx-ext-sse@2.2.4" \
  "https://cdn.jsdelivr.net/npm/htmx-ext-sse@2.2.4/sse.min.js" \
  "static/vendor/sse.min.js" \
  "ae3ab4747accd148da53626aec194187544b301899de0afc0576c43af34c95ac"

fetch "@alpinejs/csp@3.15.12" \
  "https://cdn.jsdelivr.net/npm/@alpinejs/csp@3.15.12/dist/cdn.min.js" \
  "static/vendor/alpine-csp.min.js" \
  "566167134bb2347110904e2ced6e816d2e8d837200c158f98b72372b3bb0b9a6"

fetch "@alpinejs/focus@3.15.12" \
  "https://cdn.jsdelivr.net/npm/@alpinejs/focus@3.15.12/dist/cdn.min.js" \
  "static/vendor/alpine-focus.min.js" \
  "ea7e215444f5110619549621cd0760cedfe273f708b144d4e658a87b702555f9"

fetch "@clerk/clerk-js@5.127.1" \
  "https://cdn.jsdelivr.net/npm/@clerk/clerk-js@5.127.1/dist/clerk.browser.js" \
  "static/vendor/clerk.browser.js" \
  "d92e69c91eeb10ec1558b79376a35520ead6e358811319366c6c28a4fb88d5a0"

fetch "inter-variable@5.3.0" \
  "https://cdn.jsdelivr.net/npm/@fontsource-variable/inter@5.3.0/files/inter-latin-wght-normal.woff2" \
  "static/fonts/inter-var.woff2" \
  "3100e775e8616cd2611beecfa23a4263d7037586789b43f035236a2e6fbd4c62"

echo "vendored frontend OK"

# @clerk/clerk-js lazy chunks (components mount these on demand —
# UserButton, OrganizationSwitcher, etc. 404 without them).
CLERK_JS_VERSION="5.127.1"
CLERK_CHUNKS="
2172_clerk.browser_9854dd_5.127.1.js:916f930ffb94a362e9591e3af48b8be3ebb5ea1e1b717c0fd0da022f3b3c3aaa
4170_clerk.browser_9854dd_5.127.1.js:dddb4af77af0aecc80e4ad518f0c7028e9bee93018c37da93b2de779266b71b8
5192_clerk.browser_9854dd_5.127.1.js:4f0432108c1271e73913cb10f5d8e8e8ebf007894a1a83482db090b0f1cf8bd5
5645_clerk.browser_9854dd_5.127.1.js:2baee3cb8ccebfbff51f72322b2f8064b5082178c6a7fe3fbfb740fc89b2fab2
7067_clerk.browser_9854dd_5.127.1.js:646f981c51c2b3a814848b3e27d351746cbbb536678d1aed02f3224f49709eda
7502_clerk.browser_9854dd_5.127.1.js:3ed67ee1c2c544a5e395c2806b8c770220dad0241521b4c0e64347b2c7f7f295
apiKeys_clerk.browser_9854dd_5.127.1.js:7c32c090568bdb930f938de88aef618b89672d86ecae5b43ba7fbffab2640f9c
base-account-sdk_clerk.browser_9854dd_5.127.1.js:1d744d8e1597a940d957975e5e6ef24a9ff0242f946546f57dece5e0dd84eec0
blankcaptcha_clerk.browser_9854dd_5.127.1.js:465ccb422630060b6769b076acc53c644d6c896e1eb93201e221732d40ed0157
checkout_clerk.browser_9854dd_5.127.1.js:324e0adc9e21caefd908bbfc1690058ee1be5acb3852de4ed929fc8528bcdf93
coinbase-wallet-sdk_clerk.browser_9854dd_5.127.1.js:3c7ea5aa2a7deef9ff116efacafcf6c3dd629cce6bb7d919452cc2dfeef53bdc
cookieSuffix_clerk.browser_9854dd_5.127.1.js:c86561760035abbebeb3014bcdb7fa3fd825083697a8bc0993bef488d5ec593e
copy-api-key-modal_clerk.browser_9854dd_5.127.1.js:e3dc6ef912779925c4962f87a93305e7d3c27f12c4f67820253d7a6488a2659a
createorganization_clerk.browser_9854dd_5.127.1.js:14e20e9911465ecb728f124c36a4ee904338d90470e2743e87534426f943ea55
enableOrganizationsPrompt_clerk.browser_9854dd_5.127.1.js:f4cef545f77a3cb6bce8466ff70ab699d7631f4f388398f60206394776a13cf7
framework_clerk.browser_9854dd_5.127.1.js:2836ab217b40db69b67ced179d04727fbd1f099e48608b15da409439a41ad7df
impersonationfab_clerk.browser_9854dd_5.127.1.js:cd91602f0eb261cdd596c9b255ca8d40b4760b6f7f3eb1599cd17af59bee36e7
keylessPrompt_clerk.browser_9854dd_5.127.1.js:eff117abda6ca0b8cc40c95607b8334af246643951633f0e17af219e7c57affc
oauthConsent_clerk.browser_9854dd_5.127.1.js:a7b9f5ac327dc085665b65414e2dbc38a8832f0a22090e95bd694599d8407059
onetap_clerk.browser_9854dd_5.127.1.js:6a8451042b82e36fcf4f789796aac6c8f9b50e9370c42aef4151694631172869
op-api-keys-page_clerk.browser_9854dd_5.127.1.js:8f38532d9cb4af9694682c6811dc463bff860cc001cb5734604a4fd1913406e5
op-billing-page_clerk.browser_9854dd_5.127.1.js:91b762eade20badc1e57b1410c336c2038ddbe0c1cef3085b094733db683e17a
op-plans-page_clerk.browser_9854dd_5.127.1.js:ab8fe46367e6e93982a05381d890b04be8ac0b9175fbd14b63bf4e71bd74bb7a
organizationlist_clerk.browser_9854dd_5.127.1.js:54e91ecbc784313ea9eabdc77032a8ef09464464901e69dea299047a67e27990
organizationprofile_clerk.browser_9854dd_5.127.1.js:cea5cf371afd6f2e2b8e14d8b542c5542fa9781443c9c53ec052f0e522fb56ec
organizationswitcher_clerk.browser_9854dd_5.127.1.js:b3d0a2d4e3e3c3b903511cdb914b88b784f37bc95e35919c61cca418a77be821
payment-attempt-page_clerk.browser_9854dd_5.127.1.js:e5abf101e93d624f96e9397a068d57341c5170e29ef7bea0dadfdc373a20e14b
planDetails_clerk.browser_9854dd_5.127.1.js:14053cf5a433b75d7c49f956486a111b1337e5fa3a525c8ff13a9400f0666d83
prefetchorganizationlist_clerk.browser_9854dd_5.127.1.js:618d9788a5c8ea990ea7f452386698258273aed4582e39e823ea3ac5b3679332
pricingTable_clerk.browser_9854dd_5.127.1.js:f3819cc8a3eb3666444d1dea353255a7a137bb4a6adf1fd04a9e43f541ff164f
query-core-vendors_clerk.browser_9854dd_5.127.1.js:ab2980ce8264feffe67e37b002cd3679ca6822cdadd1b2f1ca3e330f3b5b7781
revoke-api-key-modal_clerk.browser_9854dd_5.127.1.js:96b717bde9f29d99397a7607dc7534b2ef01a27ad5a66f1edcbf4cee5ea57d07
sessionTasks_clerk.browser_9854dd_5.127.1.js:a347342ff63c3a1322494ae037f98b527cb249ba7f25c78def8fe4eaecb207fb
signin_clerk.browser_9854dd_5.127.1.js:e0d6d26d6687c6472290045316637e300dc5d73b917f5a62f7ca7d623eb78d49
signup_clerk.browser_9854dd_5.127.1.js:e0eff5cb69e6f5810e26779868ef67d821b349b55f7b43c25f612bf7be3ceebb
statement-page_clerk.browser_9854dd_5.127.1.js:c978b6ba6d3d955425f879e9687ca378225395328e38fb9b860e911edba4ce9f
stripe-vendors_clerk.browser_9854dd_5.127.1.js:40f0fb24138029634cca3ec5afee64d4f379ac1b3fd7d2ffc6bcc7e90f604199
subscriptionDetails_clerk.browser_9854dd_5.127.1.js:bde9daae14c6daca721c33351876beb0298c8f2a2ddfe8af8ddca31c64d7f8ae
taskChooseOrganization_clerk.browser_9854dd_5.127.1.js:7930426f21930a239334a5021628eecdbe5a7ba771aa5690208e239c8e818572
taskResetPassword_clerk.browser_9854dd_5.127.1.js:93a875eeb8becfe468e94e0dda69e4e27bd009c169caf1360fd7375c1dc38e81
taskSetupMFA_clerk.browser_9854dd_5.127.1.js:96f722d38b4963bafc8fe96fde550a77c1f56175b3d97e2e2004de82be53c597
ui-common_clerk.browser_9854dd_5.127.1.js:5f3aa62f390a95e0305c4fb6ed6b11bdb09a69da8ba334db3437c42e48a91997
up-api-keys-page_clerk.browser_9854dd_5.127.1.js:7bf78a12d6b97d235b272389e1ed03890a32bc20de45fb8e80785187f188b6d7
up-billing-page_clerk.browser_9854dd_5.127.1.js:3962faa07beddb56ce813e62f694bd3a63973a11f5dae7c4d7d3ebb18ce9075d
up-plans-page_clerk.browser_9854dd_5.127.1.js:5b6f9f6a6def6aeae904899e2d7edc22b1e25b8b26bec916f9ca32bb72fb1e8c
useravatar_clerk.browser_9854dd_5.127.1.js:2295f1b77fd26b01266781f83d1511c9a0b0b6702078a7a64ad41597f9b5b221
userbutton_clerk.browser_9854dd_5.127.1.js:0329692a0a36859ad4d4e1a583fd81acfbb333b52342995152e2df72de088bf1
userprofile_clerk.browser_9854dd_5.127.1.js:b1e426d5667943764ce739a231ac1de6192ac5cbbb50643519480f58ace0c44f
userverification_clerk.browser_9854dd_5.127.1.js:5c7a9558b60e1950a7b3c09c5a0ca3e222d3efaa964596df2dd55ad78992d9ee
vendors_clerk.browser_9854dd_5.127.1.js:7815e1a4287c67528b9ebd966b22499041e637158573b52476d6d801f2572cf7
waitlist_clerk.browser_9854dd_5.127.1.js:3352ddf2516675fe6a3a8f3d03f08f59951a478ebcd2364fed08b97daf3d8302
web3-solana-wallet-buttons_clerk.browser_9854dd_5.127.1.js:5527aa95afb32b902504f5bdd67098ed738655c95a706218a1eb809421a4fa47
zxcvbn-common_clerk.browser_9854dd_5.127.1.js:9ee042f6cf8bcb1cbcdf5b9ad1131ad1e3ba6940bb045014df2a32dfa85271f0
zxcvbn-ts-core_clerk.browser_9854dd_5.127.1.js:06ec41cbc1c41d9db68b33704b67a1e8e247e98709d52186aa4df9630ee5ad9d
"

for entry in ${CLERK_CHUNKS}; do
  name="${entry%%:*}"; sha="${entry##*:}"
  fetch "clerk-js chunk ${name}" \
    "https://cdn.jsdelivr.net/npm/@clerk/clerk-js@${CLERK_JS_VERSION}/dist/${name}" \
    "static/vendor/${name}" \
    "${sha}"
done
