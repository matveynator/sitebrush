<?php


/**
 * Base static class for performing query and update operations on the 'media' table.
 *
 * 
 *
 * @package    propel.generator.default.om
 */
abstract class BaseMediaPeer {

	/** the default database name for this class */
	const DATABASE_NAME = 'brain';

	/** the table name for this class */
	const TABLE_NAME = 'media';

	/** the related Propel class for this table */
	const OM_CLASS = 'Media';

	/** A class that can be returned by this peer. */
	const CLASS_DEFAULT = 'default.Media';

	/** the related TableMap class for this table */
	const TM_CLASS = 'MediaTableMap';
	
	/** The total number of columns. */
	const NUM_COLUMNS = 17;

	/** The number of lazy-loaded columns. */
	const NUM_LAZY_LOAD_COLUMNS = 0;

	/** the column name for the ID field */
	const ID = 'media.ID';

	/** the column name for the TYPE field */
	const TYPE = 'media.TYPE';

	/** the column name for the HASH field */
	const HASH = 'media.HASH';

	/** the column name for the ORIGINALHASH field */
	const ORIGINALHASH = 'media.ORIGINALHASH';

	/** the column name for the FORMAT field */
	const FORMAT = 'media.FORMAT';

	/** the column name for the WIDTH field */
	const WIDTH = 'media.WIDTH';

	/** the column name for the HEIGHT field */
	const HEIGHT = 'media.HEIGHT';

	/** the column name for the STATUS field */
	const STATUS = 'media.STATUS';

	/** the column name for the DOMAIN field */
	const DOMAIN = 'media.DOMAIN';

	/** the column name for the DAY field */
	const DAY = 'media.DAY';

	/** the column name for the DATE field */
	const DATE = 'media.DATE';

	/** the column name for the SIZESARRAY field */
	const SIZESARRAY = 'media.SIZESARRAY';

	/** the column name for the RATING field */
	const RATING = 'media.RATING';

	/** the column name for the RATINGCOUNT field */
	const RATINGCOUNT = 'media.RATINGCOUNT';

	/** the column name for the RATINGIP field */
	const RATINGIP = 'media.RATINGIP';

	/** the column name for the VIEWS field */
	const VIEWS = 'media.VIEWS';

	/** the column name for the BYTESUSED field */
	const BYTESUSED = 'media.BYTESUSED';

	/** The default string format for model objects of the related table **/
	const DEFAULT_STRING_FORMAT = 'YAML';
	
	/**
	 * An identiy map to hold any loaded instances of Media objects.
	 * This must be public so that other peer classes can access this when hydrating from JOIN
	 * queries.
	 * @var        array Media[]
	 */
	public static $instances = array();


	/**
	 * holds an array of fieldnames
	 *
	 * first dimension keys are the type constants
	 * e.g. self::$fieldNames[self::TYPE_PHPNAME][0] = 'Id'
	 */
	protected static $fieldNames = array (
		BasePeer::TYPE_PHPNAME => array ('Id', 'Type', 'Hash', 'OriginalHash', 'Format', 'Width', 'Height', 'Status', 'Domain', 'Day', 'Date', 'SizesArray', 'Rating', 'RatingCount', 'RatingIp', 'Views', 'BytesUsed', ),
		BasePeer::TYPE_STUDLYPHPNAME => array ('id', 'type', 'hash', 'originalHash', 'format', 'width', 'height', 'status', 'domain', 'day', 'date', 'sizesArray', 'rating', 'ratingCount', 'ratingIp', 'views', 'bytesUsed', ),
		BasePeer::TYPE_COLNAME => array (self::ID, self::TYPE, self::HASH, self::ORIGINALHASH, self::FORMAT, self::WIDTH, self::HEIGHT, self::STATUS, self::DOMAIN, self::DAY, self::DATE, self::SIZESARRAY, self::RATING, self::RATINGCOUNT, self::RATINGIP, self::VIEWS, self::BYTESUSED, ),
		BasePeer::TYPE_RAW_COLNAME => array ('ID', 'TYPE', 'HASH', 'ORIGINALHASH', 'FORMAT', 'WIDTH', 'HEIGHT', 'STATUS', 'DOMAIN', 'DAY', 'DATE', 'SIZESARRAY', 'RATING', 'RATINGCOUNT', 'RATINGIP', 'VIEWS', 'BYTESUSED', ),
		BasePeer::TYPE_FIELDNAME => array ('id', 'type', 'hash', 'originalhash', 'format', 'width', 'height', 'status', 'domain', 'day', 'date', 'sizesarray', 'rating', 'ratingcount', 'ratingip', 'views', 'bytesused', ),
		BasePeer::TYPE_NUM => array (0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, )
	);

	/**
	 * holds an array of keys for quick access to the fieldnames array
	 *
	 * first dimension keys are the type constants
	 * e.g. self::$fieldNames[BasePeer::TYPE_PHPNAME]['Id'] = 0
	 */
	protected static $fieldKeys = array (
		BasePeer::TYPE_PHPNAME => array ('Id' => 0, 'Type' => 1, 'Hash' => 2, 'OriginalHash' => 3, 'Format' => 4, 'Width' => 5, 'Height' => 6, 'Status' => 7, 'Domain' => 8, 'Day' => 9, 'Date' => 10, 'SizesArray' => 11, 'Rating' => 12, 'RatingCount' => 13, 'RatingIp' => 14, 'Views' => 15, 'BytesUsed' => 16, ),
		BasePeer::TYPE_STUDLYPHPNAME => array ('id' => 0, 'type' => 1, 'hash' => 2, 'originalHash' => 3, 'format' => 4, 'width' => 5, 'height' => 6, 'status' => 7, 'domain' => 8, 'day' => 9, 'date' => 10, 'sizesArray' => 11, 'rating' => 12, 'ratingCount' => 13, 'ratingIp' => 14, 'views' => 15, 'bytesUsed' => 16, ),
		BasePeer::TYPE_COLNAME => array (self::ID => 0, self::TYPE => 1, self::HASH => 2, self::ORIGINALHASH => 3, self::FORMAT => 4, self::WIDTH => 5, self::HEIGHT => 6, self::STATUS => 7, self::DOMAIN => 8, self::DAY => 9, self::DATE => 10, self::SIZESARRAY => 11, self::RATING => 12, self::RATINGCOUNT => 13, self::RATINGIP => 14, self::VIEWS => 15, self::BYTESUSED => 16, ),
		BasePeer::TYPE_RAW_COLNAME => array ('ID' => 0, 'TYPE' => 1, 'HASH' => 2, 'ORIGINALHASH' => 3, 'FORMAT' => 4, 'WIDTH' => 5, 'HEIGHT' => 6, 'STATUS' => 7, 'DOMAIN' => 8, 'DAY' => 9, 'DATE' => 10, 'SIZESARRAY' => 11, 'RATING' => 12, 'RATINGCOUNT' => 13, 'RATINGIP' => 14, 'VIEWS' => 15, 'BYTESUSED' => 16, ),
		BasePeer::TYPE_FIELDNAME => array ('id' => 0, 'type' => 1, 'hash' => 2, 'originalhash' => 3, 'format' => 4, 'width' => 5, 'height' => 6, 'status' => 7, 'domain' => 8, 'day' => 9, 'date' => 10, 'sizesarray' => 11, 'rating' => 12, 'ratingcount' => 13, 'ratingip' => 14, 'views' => 15, 'bytesused' => 16, ),
		BasePeer::TYPE_NUM => array (0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, )
	);

	/**
	 * Translates a fieldname to another type
	 *
	 * @param      string $name field name
	 * @param      string $fromType One of the class type constants BasePeer::TYPE_PHPNAME, BasePeer::TYPE_STUDLYPHPNAME
	 *                         BasePeer::TYPE_COLNAME, BasePeer::TYPE_FIELDNAME, BasePeer::TYPE_NUM
	 * @param      string $toType   One of the class type constants
	 * @return     string translated name of the field.
	 * @throws     PropelException - if the specified name could not be found in the fieldname mappings.
	 */
	static public function translateFieldName($name, $fromType, $toType)
	{
		$toNames = self::getFieldNames($toType);
		$key = isset(self::$fieldKeys[$fromType][$name]) ? self::$fieldKeys[$fromType][$name] : null;
		if ($key === null) {
			throw new PropelException("'$name' could not be found in the field names of type '$fromType'. These are: " . print_r(self::$fieldKeys[$fromType], true));
		}
		return $toNames[$key];
	}

	/**
	 * Returns an array of field names.
	 *
	 * @param      string $type The type of fieldnames to return:
	 *                      One of the class type constants BasePeer::TYPE_PHPNAME, BasePeer::TYPE_STUDLYPHPNAME
	 *                      BasePeer::TYPE_COLNAME, BasePeer::TYPE_FIELDNAME, BasePeer::TYPE_NUM
	 * @return     array A list of field names
	 */

	static public function getFieldNames($type = BasePeer::TYPE_PHPNAME)
	{
		if (!array_key_exists($type, self::$fieldNames)) {
			throw new PropelException('Method getFieldNames() expects the parameter $type to be one of the class constants BasePeer::TYPE_PHPNAME, BasePeer::TYPE_STUDLYPHPNAME, BasePeer::TYPE_COLNAME, BasePeer::TYPE_FIELDNAME, BasePeer::TYPE_NUM. ' . $type . ' was given.');
		}
		return self::$fieldNames[$type];
	}

	/**
	 * Convenience method which changes table.column to alias.column.
	 *
	 * Using this method you can maintain SQL abstraction while using column aliases.
	 * <code>
	 *		$c->addAlias("alias1", TablePeer::TABLE_NAME);
	 *		$c->addJoin(TablePeer::alias("alias1", TablePeer::PRIMARY_KEY_COLUMN), TablePeer::PRIMARY_KEY_COLUMN);
	 * </code>
	 * @param      string $alias The alias for the current table.
	 * @param      string $column The column name for current table. (i.e. MediaPeer::COLUMN_NAME).
	 * @return     string
	 */
	public static function alias($alias, $column)
	{
		return str_replace(MediaPeer::TABLE_NAME.'.', $alias.'.', $column);
	}

	/**
	 * Add all the columns needed to create a new object.
	 *
	 * Note: any columns that were marked with lazyLoad="true" in the
	 * XML schema will not be added to the select list and only loaded
	 * on demand.
	 *
	 * @param      Criteria $criteria object containing the columns to add.
	 * @param      string   $alias    optional table alias
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function addSelectColumns(Criteria $criteria, $alias = null)
	{
		if (null === $alias) {
			$criteria->addSelectColumn(MediaPeer::ID);
			$criteria->addSelectColumn(MediaPeer::TYPE);
			$criteria->addSelectColumn(MediaPeer::HASH);
			$criteria->addSelectColumn(MediaPeer::ORIGINALHASH);
			$criteria->addSelectColumn(MediaPeer::FORMAT);
			$criteria->addSelectColumn(MediaPeer::WIDTH);
			$criteria->addSelectColumn(MediaPeer::HEIGHT);
			$criteria->addSelectColumn(MediaPeer::STATUS);
			$criteria->addSelectColumn(MediaPeer::DOMAIN);
			$criteria->addSelectColumn(MediaPeer::DAY);
			$criteria->addSelectColumn(MediaPeer::DATE);
			$criteria->addSelectColumn(MediaPeer::SIZESARRAY);
			$criteria->addSelectColumn(MediaPeer::RATING);
			$criteria->addSelectColumn(MediaPeer::RATINGCOUNT);
			$criteria->addSelectColumn(MediaPeer::RATINGIP);
			$criteria->addSelectColumn(MediaPeer::VIEWS);
			$criteria->addSelectColumn(MediaPeer::BYTESUSED);
		} else {
			$criteria->addSelectColumn($alias . '.ID');
			$criteria->addSelectColumn($alias . '.TYPE');
			$criteria->addSelectColumn($alias . '.HASH');
			$criteria->addSelectColumn($alias . '.ORIGINALHASH');
			$criteria->addSelectColumn($alias . '.FORMAT');
			$criteria->addSelectColumn($alias . '.WIDTH');
			$criteria->addSelectColumn($alias . '.HEIGHT');
			$criteria->addSelectColumn($alias . '.STATUS');
			$criteria->addSelectColumn($alias . '.DOMAIN');
			$criteria->addSelectColumn($alias . '.DAY');
			$criteria->addSelectColumn($alias . '.DATE');
			$criteria->addSelectColumn($alias . '.SIZESARRAY');
			$criteria->addSelectColumn($alias . '.RATING');
			$criteria->addSelectColumn($alias . '.RATINGCOUNT');
			$criteria->addSelectColumn($alias . '.RATINGIP');
			$criteria->addSelectColumn($alias . '.VIEWS');
			$criteria->addSelectColumn($alias . '.BYTESUSED');
		}
	}

	/**
	 * Returns the number of rows matching criteria.
	 *
	 * @param      Criteria $criteria
	 * @param      boolean $distinct Whether to select only distinct columns; deprecated: use Criteria->setDistinct() instead.
	 * @param      PropelPDO $con
	 * @return     int Number of matching rows.
	 */
	public static function doCount(Criteria $criteria, $distinct = false, PropelPDO $con = null)
	{
		// we may modify criteria, so copy it first
		$criteria = clone $criteria;

		// We need to set the primary table name, since in the case that there are no WHERE columns
		// it will be impossible for the BasePeer::createSelectSql() method to determine which
		// tables go into the FROM clause.
		$criteria->setPrimaryTableName(MediaPeer::TABLE_NAME);

		if ($distinct && !in_array(Criteria::DISTINCT, $criteria->getSelectModifiers())) {
			$criteria->setDistinct();
		}

		if (!$criteria->hasSelectClause()) {
			MediaPeer::addSelectColumns($criteria);
		}

		$criteria->clearOrderByColumns(); // ORDER BY won't ever affect the count
		$criteria->setDbName(self::DATABASE_NAME); // Set the correct dbName

		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_READ);
		}
		// BasePeer returns a PDOStatement
		$stmt = BasePeer::doCount($criteria, $con);

		if ($row = $stmt->fetch(PDO::FETCH_NUM)) {
			$count = (int) $row[0];
		} else {
			$count = 0; // no rows returned; we infer that means 0 matches.
		}
		$stmt->closeCursor();
		return $count;
	}
	/**
	 * Method to select one object from the DB.
	 *
	 * @param      Criteria $criteria object used to create the SELECT statement.
	 * @param      PropelPDO $con
	 * @return     Media
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function doSelectOne(Criteria $criteria, PropelPDO $con = null)
	{
		$critcopy = clone $criteria;
		$critcopy->setLimit(1);
		$objects = MediaPeer::doSelect($critcopy, $con);
		if ($objects) {
			return $objects[0];
		}
		return null;
	}
	/**
	 * Method to do selects.
	 *
	 * @param      Criteria $criteria The Criteria object used to build the SELECT statement.
	 * @param      PropelPDO $con
	 * @return     array Array of selected Objects
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function doSelect(Criteria $criteria, PropelPDO $con = null)
	{
		return MediaPeer::populateObjects(MediaPeer::doSelectStmt($criteria, $con));
	}
	/**
	 * Prepares the Criteria object and uses the parent doSelect() method to execute a PDOStatement.
	 *
	 * Use this method directly if you want to work with an executed statement durirectly (for example
	 * to perform your own object hydration).
	 *
	 * @param      Criteria $criteria The Criteria object used to build the SELECT statement.
	 * @param      PropelPDO $con The connection to use
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 * @return     PDOStatement The executed PDOStatement object.
	 * @see        BasePeer::doSelect()
	 */
	public static function doSelectStmt(Criteria $criteria, PropelPDO $con = null)
	{
		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_READ);
		}

		if (!$criteria->hasSelectClause()) {
			$criteria = clone $criteria;
			MediaPeer::addSelectColumns($criteria);
		}

		// Set the correct dbName
		$criteria->setDbName(self::DATABASE_NAME);

		// BasePeer returns a PDOStatement
		return BasePeer::doSelect($criteria, $con);
	}
	/**
	 * Adds an object to the instance pool.
	 *
	 * Propel keeps cached copies of objects in an instance pool when they are retrieved
	 * from the database.  In some cases -- especially when you override doSelect*()
	 * methods in your stub classes -- you may need to explicitly add objects
	 * to the cache in order to ensure that the same objects are always returned by doSelect*()
	 * and retrieveByPK*() calls.
	 *
	 * @param      Media $value A Media object.
	 * @param      string $key (optional) key to use for instance map (for performance boost if key was already calculated externally).
	 */
	public static function addInstanceToPool($obj, $key = null)
	{
		if (Propel::isInstancePoolingEnabled()) {
			if ($key === null) {
				$key = (string) $obj->getId();
			} // if key === null
			self::$instances[$key] = $obj;
		}
	}

	/**
	 * Removes an object from the instance pool.
	 *
	 * Propel keeps cached copies of objects in an instance pool when they are retrieved
	 * from the database.  In some cases -- especially when you override doDelete
	 * methods in your stub classes -- you may need to explicitly remove objects
	 * from the cache in order to prevent returning objects that no longer exist.
	 *
	 * @param      mixed $value A Media object or a primary key value.
	 */
	public static function removeInstanceFromPool($value)
	{
		if (Propel::isInstancePoolingEnabled() && $value !== null) {
			if (is_object($value) && $value instanceof Media) {
				$key = (string) $value->getId();
			} elseif (is_scalar($value)) {
				// assume we've been passed a primary key
				$key = (string) $value;
			} else {
				$e = new PropelException("Invalid value passed to removeInstanceFromPool().  Expected primary key or Media object; got " . (is_object($value) ? get_class($value) . ' object.' : var_export($value,true)));
				throw $e;
			}

			unset(self::$instances[$key]);
		}
	} // removeInstanceFromPool()

	/**
	 * Retrieves a string version of the primary key from the DB resultset row that can be used to uniquely identify a row in this table.
	 *
	 * For tables with a single-column primary key, that simple pkey value will be returned.  For tables with
	 * a multi-column primary key, a serialize()d version of the primary key will be returned.
	 *
	 * @param      string $key The key (@see getPrimaryKeyHash()) for this instance.
	 * @return     Media Found object or NULL if 1) no instance exists for specified key or 2) instance pooling has been disabled.
	 * @see        getPrimaryKeyHash()
	 */
	public static function getInstanceFromPool($key)
	{
		if (Propel::isInstancePoolingEnabled()) {
			if (isset(self::$instances[$key])) {
				return self::$instances[$key];
			}
		}
		return null; // just to be explicit
	}
	
	/**
	 * Clear the instance pool.
	 *
	 * @return     void
	 */
	public static function clearInstancePool()
	{
		self::$instances = array();
	}
	
	/**
	 * Method to invalidate the instance pool of all tables related to media
	 * by a foreign key with ON DELETE CASCADE
	 */
	public static function clearRelatedInstancePool()
	{
	}

	/**
	 * Retrieves a string version of the primary key from the DB resultset row that can be used to uniquely identify a row in this table.
	 *
	 * For tables with a single-column primary key, that simple pkey value will be returned.  For tables with
	 * a multi-column primary key, a serialize()d version of the primary key will be returned.
	 *
	 * @param      array $row PropelPDO resultset row.
	 * @param      int $startcol The 0-based offset for reading from the resultset row.
	 * @return     string A string version of PK or NULL if the components of primary key in result array are all null.
	 */
	public static function getPrimaryKeyHashFromRow($row, $startcol = 0)
	{
		// If the PK cannot be derived from the row, return NULL.
		if ($row[$startcol] === null) {
			return null;
		}
		return (string) $row[$startcol];
	}

	/**
	 * Retrieves the primary key from the DB resultset row 
	 * For tables with a single-column primary key, that simple pkey value will be returned.  For tables with
	 * a multi-column primary key, an array of the primary key columns will be returned.
	 *
	 * @param      array $row PropelPDO resultset row.
	 * @param      int $startcol The 0-based offset for reading from the resultset row.
	 * @return     mixed The primary key of the row
	 */
	public static function getPrimaryKeyFromRow($row, $startcol = 0)
	{
		return (int) $row[$startcol];
	}
	
	/**
	 * The returned array will contain objects of the default type or
	 * objects that inherit from the default.
	 *
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function populateObjects(PDOStatement $stmt)
	{
		$results = array();
	
		// set the class once to avoid overhead in the loop
		$cls = MediaPeer::getOMClass(false);
		// populate the object(s)
		while ($row = $stmt->fetch(PDO::FETCH_NUM)) {
			$key = MediaPeer::getPrimaryKeyHashFromRow($row, 0);
			if (null !== ($obj = MediaPeer::getInstanceFromPool($key))) {
				// We no longer rehydrate the object, since this can cause data loss.
				// See http://www.propelorm.org/ticket/509
				// $obj->hydrate($row, 0, true); // rehydrate
				$results[] = $obj;
			} else {
				$obj = new $cls();
				$obj->hydrate($row);
				$results[] = $obj;
				MediaPeer::addInstanceToPool($obj, $key);
			} // if key exists
		}
		$stmt->closeCursor();
		return $results;
	}
	/**
	 * Populates an object of the default type or an object that inherit from the default.
	 *
	 * @param      array $row PropelPDO resultset row.
	 * @param      int $startcol The 0-based offset for reading from the resultset row.
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 * @return     array (Media object, last column rank)
	 */
	public static function populateObject($row, $startcol = 0)
	{
		$key = MediaPeer::getPrimaryKeyHashFromRow($row, $startcol);
		if (null !== ($obj = MediaPeer::getInstanceFromPool($key))) {
			// We no longer rehydrate the object, since this can cause data loss.
			// See http://www.propelorm.org/ticket/509
			// $obj->hydrate($row, $startcol, true); // rehydrate
			$col = $startcol + MediaPeer::NUM_COLUMNS;
		} else {
			$cls = MediaPeer::OM_CLASS;
			$obj = new $cls();
			$col = $obj->hydrate($row, $startcol);
			MediaPeer::addInstanceToPool($obj, $key);
		}
		return array($obj, $col);
	}
	/**
	 * Returns the TableMap related to this peer.
	 * This method is not needed for general use but a specific application could have a need.
	 * @return     TableMap
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function getTableMap()
	{
		return Propel::getDatabaseMap(self::DATABASE_NAME)->getTable(self::TABLE_NAME);
	}

	/**
	 * Add a TableMap instance to the database for this peer class.
	 */
	public static function buildTableMap()
	{
	  $dbMap = Propel::getDatabaseMap(BaseMediaPeer::DATABASE_NAME);
	  if (!$dbMap->hasTable(BaseMediaPeer::TABLE_NAME))
	  {
	    $dbMap->addTableObject(new MediaTableMap());
	  }
	}

	/**
	 * The class that the Peer will make instances of.
	 *
	 * If $withPrefix is true, the returned path
	 * uses a dot-path notation which is tranalted into a path
	 * relative to a location on the PHP include_path.
	 * (e.g. path.to.MyClass -> 'path/to/MyClass.php')
	 *
	 * @param      boolean $withPrefix Whether or not to return the path with the class name
	 * @return     string path.to.ClassName
	 */
	public static function getOMClass($withPrefix = true)
	{
		return $withPrefix ? MediaPeer::CLASS_DEFAULT : MediaPeer::OM_CLASS;
	}

	/**
	 * Method perform an INSERT on the database, given a Media or Criteria object.
	 *
	 * @param      mixed $values Criteria or Media object containing data that is used to create the INSERT statement.
	 * @param      PropelPDO $con the PropelPDO connection to use
	 * @return     mixed The new primary key.
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function doInsert($values, PropelPDO $con = null)
	{
		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_WRITE);
		}

		if ($values instanceof Criteria) {
			$criteria = clone $values; // rename for clarity
		} else {
			$criteria = $values->buildCriteria(); // build Criteria from Media object
		}

		if ($criteria->containsKey(MediaPeer::ID) && $criteria->keyContainsValue(MediaPeer::ID) ) {
			throw new PropelException('Cannot insert a value for auto-increment primary key ('.MediaPeer::ID.')');
		}


		// Set the correct dbName
		$criteria->setDbName(self::DATABASE_NAME);

		try {
			// use transaction because $criteria could contain info
			// for more than one table (I guess, conceivably)
			$con->beginTransaction();
			$pk = BasePeer::doInsert($criteria, $con);
			$con->commit();
		} catch(PropelException $e) {
			$con->rollBack();
			throw $e;
		}

		return $pk;
	}

	/**
	 * Method perform an UPDATE on the database, given a Media or Criteria object.
	 *
	 * @param      mixed $values Criteria or Media object containing data that is used to create the UPDATE statement.
	 * @param      PropelPDO $con The connection to use (specify PropelPDO connection object to exert more control over transactions).
	 * @return     int The number of affected rows (if supported by underlying database driver).
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function doUpdate($values, PropelPDO $con = null)
	{
		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_WRITE);
		}

		$selectCriteria = new Criteria(self::DATABASE_NAME);

		if ($values instanceof Criteria) {
			$criteria = clone $values; // rename for clarity

			$comparison = $criteria->getComparison(MediaPeer::ID);
			$value = $criteria->remove(MediaPeer::ID);
			if ($value) {
				$selectCriteria->add(MediaPeer::ID, $value, $comparison);
			} else {
				$selectCriteria->setPrimaryTableName(MediaPeer::TABLE_NAME);
			}

		} else { // $values is Media object
			$criteria = $values->buildCriteria(); // gets full criteria
			$selectCriteria = $values->buildPkeyCriteria(); // gets criteria w/ primary key(s)
		}

		// set the correct dbName
		$criteria->setDbName(self::DATABASE_NAME);

		return BasePeer::doUpdate($selectCriteria, $criteria, $con);
	}

	/**
	 * Method to DELETE all rows from the media table.
	 *
	 * @return     int The number of affected rows (if supported by underlying database driver).
	 */
	public static function doDeleteAll($con = null)
	{
		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_WRITE);
		}
		$affectedRows = 0; // initialize var to track total num of affected rows
		try {
			// use transaction because $criteria could contain info
			// for more than one table or we could emulating ON DELETE CASCADE, etc.
			$con->beginTransaction();
			$affectedRows += BasePeer::doDeleteAll(MediaPeer::TABLE_NAME, $con, MediaPeer::DATABASE_NAME);
			// Because this db requires some delete cascade/set null emulation, we have to
			// clear the cached instance *after* the emulation has happened (since
			// instances get re-added by the select statement contained therein).
			MediaPeer::clearInstancePool();
			MediaPeer::clearRelatedInstancePool();
			$con->commit();
			return $affectedRows;
		} catch (PropelException $e) {
			$con->rollBack();
			throw $e;
		}
	}

	/**
	 * Method perform a DELETE on the database, given a Media or Criteria object OR a primary key value.
	 *
	 * @param      mixed $values Criteria or Media object or primary key or array of primary keys
	 *              which is used to create the DELETE statement
	 * @param      PropelPDO $con the connection to use
	 * @return     int 	The number of affected rows (if supported by underlying database driver).  This includes CASCADE-related rows
	 *				if supported by native driver or if emulated using Propel.
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	 public static function doDelete($values, PropelPDO $con = null)
	 {
		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_WRITE);
		}

		if ($values instanceof Criteria) {
			// invalidate the cache for all objects of this type, since we have no
			// way of knowing (without running a query) what objects should be invalidated
			// from the cache based on this Criteria.
			MediaPeer::clearInstancePool();
			// rename for clarity
			$criteria = clone $values;
		} elseif ($values instanceof Media) { // it's a model object
			// invalidate the cache for this single object
			MediaPeer::removeInstanceFromPool($values);
			// create criteria based on pk values
			$criteria = $values->buildPkeyCriteria();
		} else { // it's a primary key, or an array of pks
			$criteria = new Criteria(self::DATABASE_NAME);
			$criteria->add(MediaPeer::ID, (array) $values, Criteria::IN);
			// invalidate the cache for this object(s)
			foreach ((array) $values as $singleval) {
				MediaPeer::removeInstanceFromPool($singleval);
			}
		}

		// Set the correct dbName
		$criteria->setDbName(self::DATABASE_NAME);

		$affectedRows = 0; // initialize var to track total num of affected rows

		try {
			// use transaction because $criteria could contain info
			// for more than one table or we could emulating ON DELETE CASCADE, etc.
			$con->beginTransaction();
			
			$affectedRows += BasePeer::doDelete($criteria, $con);
			MediaPeer::clearRelatedInstancePool();
			$con->commit();
			return $affectedRows;
		} catch (PropelException $e) {
			$con->rollBack();
			throw $e;
		}
	}

	/**
	 * Validates all modified columns of given Media object.
	 * If parameter $columns is either a single column name or an array of column names
	 * than only those columns are validated.
	 *
	 * NOTICE: This does not apply to primary or foreign keys for now.
	 *
	 * @param      Media $obj The object to validate.
	 * @param      mixed $cols Column name or array of column names.
	 *
	 * @return     mixed TRUE if all columns are valid or the error message of the first invalid column.
	 */
	public static function doValidate($obj, $cols = null)
	{
		$columns = array();

		if ($cols) {
			$dbMap = Propel::getDatabaseMap(MediaPeer::DATABASE_NAME);
			$tableMap = $dbMap->getTable(MediaPeer::TABLE_NAME);

			if (! is_array($cols)) {
				$cols = array($cols);
			}

			foreach ($cols as $colName) {
				if ($tableMap->containsColumn($colName)) {
					$get = 'get' . $tableMap->getColumn($colName)->getPhpName();
					$columns[$colName] = $obj->$get();
				}
			}
		} else {

		}

		return BasePeer::doValidate(MediaPeer::DATABASE_NAME, MediaPeer::TABLE_NAME, $columns);
	}

	/**
	 * Retrieve a single object by pkey.
	 *
	 * @param      int $pk the primary key.
	 * @param      PropelPDO $con the connection to use
	 * @return     Media
	 */
	public static function retrieveByPK($pk, PropelPDO $con = null)
	{

		if (null !== ($obj = MediaPeer::getInstanceFromPool((string) $pk))) {
			return $obj;
		}

		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_READ);
		}

		$criteria = new Criteria(MediaPeer::DATABASE_NAME);
		$criteria->add(MediaPeer::ID, $pk);

		$v = MediaPeer::doSelect($criteria, $con);

		return !empty($v) > 0 ? $v[0] : null;
	}

	/**
	 * Retrieve multiple objects by pkey.
	 *
	 * @param      array $pks List of primary keys
	 * @param      PropelPDO $con the connection to use
	 * @throws     PropelException Any exceptions caught during processing will be
	 *		 rethrown wrapped into a PropelException.
	 */
	public static function retrieveByPKs($pks, PropelPDO $con = null)
	{
		if ($con === null) {
			$con = Propel::getConnection(MediaPeer::DATABASE_NAME, Propel::CONNECTION_READ);
		}

		$objs = null;
		if (empty($pks)) {
			$objs = array();
		} else {
			$criteria = new Criteria(MediaPeer::DATABASE_NAME);
			$criteria->add(MediaPeer::ID, $pks, Criteria::IN);
			$objs = MediaPeer::doSelect($criteria, $con);
		}
		return $objs;
	}

} // BaseMediaPeer

// This is the static code needed to register the TableMap for this table with the main Propel class.
//
BaseMediaPeer::buildTableMap();

