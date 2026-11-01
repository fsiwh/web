module.exports = {
  branches: ['main'],
  tagFormat: 'v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      preset: 'conventionalcommits',
      releaseRules: [
        { breaking: true, release: 'major' },
        { type: 'chore', scope: 'deps', release: 'patch' },
        { type: 'refactor', scope: 'storage', release: 'patch' },
      ],
    }],
    ['@semantic-release/github', {
      releaseBodyTemplate: '<%= "" %>',
      successComment: false,
      failComment: false,
      failTitle: false,
      releasedLabels: false,
    }],
    ['@semantic-release/exec', {
      execCwd: '../..',
      prepareCmd: `set -eu
        expected=$(cat "$RELEASE_ARTIFACT/image-digest")
        skopeo --insecure-policy copy --preserve-digests --authfile "$HOME/.docker/config.json" \
          "oci:$RELEASE_ARTIFACT/image:web" "docker://$IMAGE_PREFIX:\${nextRelease.gitTag}"
        published=$(skopeo inspect --authfile "$HOME/.docker/config.json" \
          --format '{{.Digest}}' "docker://$IMAGE_PREFIX:\${nextRelease.gitTag}")
        test "$published" = "$expected"
        cd "$RELEASE_ARTIFACT"
        oras attach --artifact-type application/spdx+json "$IMAGE_PREFIX@$expected" \
          web.sbom.spdx.json:application/spdx+json`,
      successCmd: 'printf "released=true\\ntag=%s\\n" "${nextRelease.gitTag}" >> "$GITHUB_OUTPUT"',
    }],
  ],
};
