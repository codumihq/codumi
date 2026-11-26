// Codumi Dart extractor.
// Uses package:analyzer (the same resolver as the Dart IDE plugins), so
// every reference is resolved exactly. Emits one JSON object per line.
// Run by the Go binary; not meant to be used directly.
import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/analysis_context.dart';
import 'package:analyzer/dart/analysis/analysis_context_collection.dart';
import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/element/element.dart';
import 'package:analyzer/dart/element/type.dart';
import 'package:analyzer/source/line_info.dart';
// ignore: implementation_imports
import 'package:analyzer/src/dart/analysis/analysis_context_collection.dart';
// ignore: implementation_imports
import 'package:analyzer/src/dart/analysis/file_byte_store.dart';
import 'package:path/path.dart' as p;

final ignore = RegExp(r'(^|/)(\.dart_tool|build|\.git|\.codumi|node_modules|ios/Pods|\.symlinks|\.fvm)(/|$)');
final generated = RegExp(r'\.(g|freezed|mocks|gr|config)\.dart$');
final httpVerb = RegExp(r'^(get|post|put|patch|delete|head)([A-Z_]|$)');
final apiLike = RegExp(r'^/(api|v\d+)(/|$)');
const tableOps = {'select', 'insert', 'update', 'upsert', 'delete'};
const storageOps = {'upload', 'uploadBinary', 'download', 'list', 'remove', 'move', 'copy', 'update', 'updateBinary', 'createSignedUrl', 'createSignedUrls', 'createSignedUploadUrl', 'uploadToSignedUrl', 'getPublicUrl'};

late String root;

void out(Map<String, Object?> o) => stdout.writeln(jsonEncode(o));

String rel(String path) => p.posix.joinAll(p.split(p.relative(path, from: root)));

bool underRoot(String path) {
  final r = p.relative(path, from: root);
  return !r.startsWith('..') && !p.isAbsolute(r) && !ignore.hasMatch(rel(path));
}

/// Normalizes an element to what Codumi names: top-level declarations and
/// Type.member. Getters/setters map to their variable, constructors to
/// their class.
Element? normalize(Element? e) {
  if (e == null) return null;
  if (e is PropertyAccessorElement) e = e.variable;
  if (e is ConstructorElement) e = e.enclosingElement;
  return e.baseElement;
}

String? nameOf(Element? e) {
  e = normalize(e);
  if (e == null) return null;
  final n = e.name;
  if (n == null || n.isEmpty) return null;
  final enc = e.enclosingElement;
  if (enc is LibraryElement) return n;
  if (enc is InstanceElement && enc.enclosingElement is LibraryElement) {
    final cn = enc.name;
    if (cn == null || cn.isEmpty) return null;
    return '$cn.$n';
  }
  return null;
}

String? fileOf(Element? e) {
  e = normalize(e);
  final src = e?.firstFragment.libraryFragment?.source.fullName;
  if (src == null || !underRoot(src)) return null;
  return rel(src);
}

String kindOf(Element e) {
  if (e is ClassElement) return 'class';
  if (e is MixinElement) return 'mixin';
  if (e is EnumElement) return 'enum';
  if (e is ExtensionElement) return 'extension';
  if (e is TypeAliasElement) return 'type';
  if (e is TopLevelFunctionElement) return 'function';
  if (e is MethodElement) return 'method';
  if (e is TopLevelVariableElement) return 'var';
  if (e is FieldElement) return 'field';
  return 'other';
}

class Walker extends RecursiveAstVisitor<void> {
  final String file;
  final LineInfo lines;
  final Set<String> declared = {};
  Walker(this.file, this.lines);

  int line(int offset) => lines.getLocation(offset).lineNumber;

  String anchor(AstNode node) {
    AstNode? n = node;
    while (n != null) {
      if (n is Declaration) {
        final name = nameOf(n.declaredFragment?.element);
        if (name != null) return name;
      }
      n = n.parent;
    }
    return '<library>';
  }

  void decl(Declaration d) {
    final frag = d.declaredFragment;
    if (frag == null) return;
    final e = normalize(frag.element);
    final name = nameOf(e);
    if (e == null || name == null || !declared.add(name)) return;
    out({'k': 'decl', 'f': file, 'name': name, 'kind': kindOf(e), 'line': line(frag.nameOffset ?? frag.offset), 'exported': !name.split('.').last.startsWith('_')});
  }

  void ref(AstNode node, Element? e, String kind) {
    final name = nameOf(e);
    final tf = fileOf(e);
    if (name == null || tf == null) return;
    out({'k': 'ref', 'f': file, 'line': line(node.offset), 'from': anchor(node), 'tf': tf, 'to': name, 'kind': kind});
  }

  @override
  void visitClassDeclaration(ClassDeclaration node) { decl(node); super.visitClassDeclaration(node); }
  @override
  void visitMixinDeclaration(MixinDeclaration node) { decl(node); super.visitMixinDeclaration(node); }
  @override
  void visitEnumDeclaration(EnumDeclaration node) { decl(node); super.visitEnumDeclaration(node); }
  @override
  void visitExtensionDeclaration(ExtensionDeclaration node) { decl(node); super.visitExtensionDeclaration(node); }
  @override
  void visitExtensionTypeDeclaration(ExtensionTypeDeclaration node) { decl(node); super.visitExtensionTypeDeclaration(node); }
  @override
  void visitFunctionDeclaration(FunctionDeclaration node) { decl(node); super.visitFunctionDeclaration(node); }
  @override
  void visitMethodDeclaration(MethodDeclaration node) { decl(node); super.visitMethodDeclaration(node); }
  @override
  void visitGenericTypeAlias(GenericTypeAlias node) { decl(node); super.visitGenericTypeAlias(node); }
  @override
  void visitVariableDeclaration(VariableDeclaration node) { decl(node); super.visitVariableDeclaration(node); }

  @override
  void visitSimpleIdentifier(SimpleIdentifier node) {
    final parent = node.parent;
    var kind = 'ref';
    if (node.thisOrAncestorOfType<Directive>() != null) {
      kind = 'import';
    } else if (parent is MethodInvocation && parent.methodName == node) {
      kind = 'call';
    }
    ref(node, node.element, kind);
    super.visitSimpleIdentifier(node);
  }

  @override
  void visitNamedType(NamedType node) {
    final parent = node.parent;
    final kind = (parent is ConstructorName && parent.parent is InstanceCreationExpression) ? 'new' : 'type';
    ref(node, node.element, kind);
    super.visitNamedType(node);
  }

  @override
  void visitMethodInvocation(MethodInvocation node) {
    final m = node.methodName.name;
    final args = node.argumentList.arguments;
    if ((m == 'from' || m == 'rpc') && args.isNotEmpty && args.first is SimpleStringLiteral) {
      final name = (args.first as SimpleStringLiteral).value;
      final target = node.realTarget;
      String? type;
      var client = target?.toSource() ?? '';
      if (m == 'rpc' && looksSupabase(target)) {
        type = 'rpc';
      } else if (m == 'from' && target != null && storageTarget(target)) {
        type = 'bucket';
      } else if (m == 'from' && looksSupabase(target)) {
        type = 'table';
      }
      if (type != null) {
        if (client.length > 60) client = client.substring(0, 60);
        out({'k': 'db', 'f': file, 'line': line(node.offset), 'from': anchor(node), 'type': type, 'name': name, 'ops': chainOps(node, type == 'bucket' ? storageOps : tableOps), 'client': client.replaceAll(RegExp(r'\s+'), '')});
      }
    }
    super.visitMethodInvocation(node);
  }

  bool storageTarget(Expression t) {
    if (t is PropertyAccess) return t.propertyName.name == 'storage';
    if (t is PrefixedIdentifier) return t.identifier.name == 'storage';
    return false;
  }

  bool looksSupabase(Expression? t) {
    if (t == null) return false;
    final type = t.staticType;
    if (type == null || type is DynamicType) return true;
    final n = type.element?.name ?? '';
    return RegExp(r'Supabase|Postgrest|SupabaseQuery').hasMatch(n);
  }

  List<String> chainOps(MethodInvocation call, Set<String> allowed) {
    final ops = <String>[];
    AstNode cur = call;
    while (true) {
      final par = cur.parent;
      if (par is MethodInvocation && par.target == cur) {
        final n = par.methodName.name;
        if (allowed.contains(n) && !ops.contains(n)) ops.add(n);
        cur = par;
      } else if (par is PropertyAccess && par.target == cur) {
        cur = par;
      } else {
        break;
      }
    }
    return ops;
  }

  void apiString(StringLiteral node, String value) {
    final path = value.replaceFirst(RegExp(r'^(\$\{\})+'), '').split('?').first;
    if (!apiLike.hasMatch(path)) return;
    var method = '';
    AstNode? n = node.parent;
    for (var i = 0; i < 6 && n != null; i++, n = n.parent) {
      final mm = n is MethodInvocation ? httpVerb.firstMatch(n.methodName.name) : null;
      if (mm != null) {
        method = mm.group(1)!.toUpperCase();
        break;
      }
    }
    out({'k': 'call_route', 'f': file, 'line': line(node.offset), 'from': anchor(node), 'path': path, 'method': method});
  }

  @override
  void visitSimpleStringLiteral(SimpleStringLiteral node) {
    if (node.parent is! StringInterpolation && node.parent is! AdjacentStrings) apiString(node, node.value);
    super.visitSimpleStringLiteral(node);
  }

  @override
  void visitStringInterpolation(StringInterpolation node) {
    final b = StringBuffer();
    for (final el in node.elements) {
      if (el is InterpolationString) {
        b.write(el.value);
      } else {
        b.write(r'${}');
      }
    }
    apiString(node, b.toString());
    super.visitStringInterpolation(node);
  }
}

Future<void> main(List<String> args) async {
  root = p.normalize(p.absolute(args.isNotEmpty ? args[0] : '.'));
  Set<String>? only;
  if (args.length > 1) {
    only = File(args[1]).readAsLinesSync().where((l) => l.isNotEmpty).toSet();
  }
  final sdk = Platform.environment['CODUMI_DART_SDK'];
  // A persistent summary cache (like the IDE's) makes re-runs fast: the SDK
  // and packages are not re-analyzed every time.
  final cacheDir = Platform.environment['CODUMI_DART_CACHE'];
  final AnalysisContextCollection collection = AnalysisContextCollectionImpl(
    includedPaths: [root],
    sdkPath: (sdk != null && sdk.isNotEmpty) ? sdk : null,
    byteStore: (cacheDir != null && cacheDir.isNotEmpty) ? EvictingFileByteStore(cacheDir, 1 << 30) : null,
  );
  var files = 0;
  final seen = <String>{};
  Future<void> walk(AnalysisContext context, String path) async {
    if (!path.endsWith('.dart') || !underRoot(path) || generated.hasMatch(path)) return;
    final f = rel(path);
    if (!seen.add(f)) return;
    final result = await context.currentSession.getResolvedUnit(path);
    if (result is! ResolvedUnitResult) return;
    files++;
    out({'k': 'file', 'f': f, 'lines': result.lineInfo.lineCount});
    result.unit.accept(Walker(f, result.lineInfo));
  }

  if (only != null) {
    // Incremental: open just the changed files instead of listing the project.
    for (final f in only) {
      final path = p.normalize(p.join(root, f));
      if (!File(path).existsSync()) continue;
      try {
        await walk(collection.contextFor(path), path);
      } on StateError {
        // not part of any analysis context (e.g. excluded by analysis_options)
      }
    }
  } else {
    for (final context in collection.contexts) {
      for (final path in context.contextRoot.analyzedFiles()) {
        await walk(context, path);
      }
    }
  }
  out({'k': 'done', 'files': files});
}
