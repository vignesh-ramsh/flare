/// Validates a brand folder and generates the per-platform branding files.
library;

export 'src/generator.dart'
    show
        renderBrand,
        writeOutputs,
        ownedDirectories,
        ownedFiles,
        pinBinaryName,
        pinnedCmakeFiles;
export 'src/loader.dart' show Brand, loadBrand;
export 'src/outputs.dart' show Outputs;
export 'src/themes.dart'
    show
        BrandTheme,
        BrandThemes,
        contrastOf,
        maxBrandThemes,
        parseThemesYaml,
        themeMinimumContrast,
        themeReadablePairs,
        themeTokenKeys;
export 'src/spec.dart'
    show BrandSpec, Report, parseBrandYaml, upstreamName, upstreamUrl;
